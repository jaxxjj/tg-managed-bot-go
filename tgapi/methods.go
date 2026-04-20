package tgapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// bodyPreview returns a bounded, newline-scrubbed preview of body,
// suitable for inclusion in error messages without spraying HTML
// across a log line.
func bodyPreview(body []byte, max int) string {
	s := string(body)
	if len(s) > max {
		s = s[:max] + "..."
	}
	return strings.ReplaceAll(s, "\n", " ")
}

// apiResponse is Telegram's envelope format. Every Bot API response is
// either `{"ok":true,"result":...}` or `{"ok":false,"error_code":...,
// "description":"..."}`; Result is decoded lazily into the caller's type.
//
// Reference: https://core.telegram.org/bots/api#making-requests
type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
	Description string          `json:"description,omitempty"`
}

// do executes a POST call to /bot<token>/<method>. When req is non-nil
// it is JSON-marshalled as the request body. When resp is non-nil the
// response's `result` field is JSON-unmarshalled into it.
//
// On non-OK responses the returned error is either a sentinel-wrapped
// [*APIError] (when [classify] recognizes the code/description) or the
// bare [*APIError]. Transport-level failures (network, JSON) are wrapped
// with fmt.Errorf so they still satisfy errors.Is for context.Canceled.
func (c *Client) do(ctx context.Context, method string, req, resp any) error {
	var body io.Reader
	if req != nil {
		buf, err := json.Marshal(req)
		if err != nil {
			return fmt.Errorf("tgapi %s: marshal request: %w", method, err)
		}
		body = bytes.NewReader(buf)
	}

	url := fmt.Sprintf("%s/bot%s/%s", c.base, c.token, method)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("tgapi %s: build request: %w", method, err)
	}
	if req != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("tgapi %s: %w", method, err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	// Read the body up-front so a decode failure can include a preview
	// of what we actually got (e.g. an HTML 502 from a proxy). The Bot
	// API's largest practical response is well under 64 KiB.
	bodyBytes, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("tgapi %s: read body (status=%d): %w", method, httpResp.StatusCode, err)
	}

	var env apiResponse
	if err := json.Unmarshal(bodyBytes, &env); err != nil {
		// Non-JSON response: almost always an intermediate proxy / LB
		// returning HTML. Include status + body preview so the caller
		// can diagnose "what returned this junk".
		return fmt.Errorf(
			"tgapi %s: decode envelope (status=%d, content_type=%q, body_preview=%q): %w",
			method, httpResp.StatusCode, httpResp.Header.Get("Content-Type"),
			bodyPreview(bodyBytes, 200), err,
		)
	}

	if !env.OK {
		apiErr := &APIError{Code: env.ErrorCode, Description: env.Description}
		if s := classify(env.ErrorCode, env.Description); s != nil {
			// Wrap BOTH the sentinel and the *APIError so callers can
			// reach either via errors.Is (sentinel) or errors.As (APIError).
			// Multiple %w verbs require Go 1.20+.
			return fmt.Errorf("%w: %w", s, apiErr)
		}
		return apiErr
	}

	if resp != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, resp); err != nil {
			return fmt.Errorf("tgapi %s: decode result: %w", method, err)
		}
	}
	return nil
}

// GetMe returns the authenticated bot's own [*User]. Use this to verify
// the token works and to read state such as
// [User.CanReadAllGroupMessages] (Privacy Mode) during reconciliation.
//
// Reference: https://core.telegram.org/bots/api#getme
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.do(ctx, "getMe", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetManagedBotToken returns the authentication token of a managed bot
// the caller manages, identified by its Telegram user ID.
//
// The caller must be a bot with Bot Management Mode enabled in
// @BotFather. Returns a wrapped [ErrBotDeactivated] if the user has
// deleted the managed bot via @BotFather.
//
// Reference: https://core.telegram.org/bots/api#getmanagedbottoken
func (c *Client) GetManagedBotToken(ctx context.Context, botID int64) (string, error) {
	req := struct {
		UserID int64 `json:"user_id"`
	}{botID}
	var token string
	if err := c.do(ctx, "getManagedBotToken", req, &token); err != nil {
		return "", err
	}
	return token, nil
}

// ReplaceManagedBotToken rotates a managed bot's authentication token.
// The previous token is revoked atomically — any subsequent call with
// the old token returns HTTP 401.
//
// Reference: https://core.telegram.org/bots/api#replacemanagedbottoken
func (c *Client) ReplaceManagedBotToken(ctx context.Context, botID int64) (string, error) {
	req := struct {
		UserID int64 `json:"user_id"`
	}{botID}
	var token string
	if err := c.do(ctx, "replaceManagedBotToken", req, &token); err != nil {
		return "", err
	}
	return token, nil
}

// GetWebhookInfo returns the current webhook configuration for this bot.
// Reconciliation uses it to detect webhook hijacking (a user-managed bot
// whose webhook is different from what the Alva-side runtime set).
//
// Reference: https://core.telegram.org/bots/api#getwebhookinfo
func (c *Client) GetWebhookInfo(ctx context.Context) (*WebhookInfo, error) {
	var info WebhookInfo
	if err := c.do(ctx, "getWebhookInfo", nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// SendMessage sends a plain-text message to the given chat. The primary
// use is the DM fallback path: when a manager receives a
// [ManagedBotCreated] event whose bot username was edited by the user
// so nonce correlation fails, the manager bot DMs the creator to
// recover.
//
// Reference: https://core.telegram.org/bots/api#sendmessage
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	req := struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}{chatID, text}
	return c.do(ctx, "sendMessage", req, nil)
}
