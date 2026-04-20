package tgapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newMock spins up an httptest.Server with the given handler and returns
// a Client wired to its URL. Shared across method tests to keep each
// case focused on its request/response shape.
func newMock(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient("test-token", WithBaseURL(srv.URL))
}

// expectMethod returns a handler that asserts the request path/method
// and delegates body-level assertions + response writing to the caller.
// Keeps method-specific tests terse and diff-friendly.
func expectMethod(t *testing.T, wantMethod, wantTGMethod string, writeJSON func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	t.Helper()
	wantPath := "/bottest-token/" + wantTGMethod
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != wantMethod {
			t.Errorf("HTTP method = %q, want %q", r.Method, wantMethod)
		}
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}
		writeJSON(w, r)
	}
}

// writeOK writes a Telegram-shaped success envelope with the given
// json-encoded result.
func writeOK(w http.ResponseWriter, result string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"ok":true,"result":`+result+`}`)
}

// writeErr writes a Telegram-shaped error envelope. The HTTP status code
// is also set to code so Telegram's real behavior is mimicked.
func writeErr(w http.ResponseWriter, code int, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	b, _ := json.Marshal(map[string]any{
		"ok":          false,
		"error_code":  code,
		"description": description,
	})
	_, _ = w.Write(b)
}

// ---------- GetMe ----------

func TestGetMe_Success(t *testing.T) {
	c := newMock(t, expectMethod(t, http.MethodPost, "getMe", func(w http.ResponseWriter, _ *http.Request) {
		writeOK(w, `{
			"id":123,
			"is_bot":true,
			"first_name":"Alva",
			"username":"alva_bot",
			"can_manage_bots":true,
			"can_read_all_group_messages":true,
			"can_join_groups":true
		}`)
	}))

	u, err := c.GetMe(context.Background())
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}
	if u.ID != 123 {
		t.Errorf("ID = %d, want 123", u.ID)
	}
	if !u.IsBot || !u.CanManageBots || !u.CanReadAllGroupMessages || !u.CanJoinGroups {
		t.Errorf("bot flags unexpected: %+v", u)
	}
	if u.Username != "alva_bot" {
		t.Errorf("Username = %q", u.Username)
	}
}

func TestGetMe_Unauthorized(t *testing.T) {
	c := newMock(t, func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, 401, "Unauthorized")
	})

	_, err := c.GetMe(context.Background())
	if !IsUnauthorized(err) {
		t.Errorf("want IsUnauthorized, got %v", err)
	}
	// Underlying APIError should still be accessible via errors.As.
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Errorf("want *APIError via errors.As, got %T", err)
	} else if apiErr.Code != 401 {
		t.Errorf("APIError.Code = %d, want 401", apiErr.Code)
	}
}

// ---------- GetManagedBotToken ----------

func TestGetManagedBotToken_Success(t *testing.T) {
	c := newMock(t, expectMethod(t, http.MethodPost, "getManagedBotToken", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UserID int64 `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode req: %v", err)
		}
		if req.UserID != 987654321 {
			t.Errorf("user_id = %d", req.UserID)
		}
		writeOK(w, `"987654321:AAEAAAAAAAAAAAAAAAAAAAAAAAAAAAA"`)
	}))

	tok, err := c.GetManagedBotToken(context.Background(), 987654321)
	if err != nil {
		t.Fatalf("GetManagedBotToken: %v", err)
	}
	if tok != "987654321:AAEAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Errorf("token = %q", tok)
	}
}

func TestGetManagedBotToken_BotDeactivated(t *testing.T) {
	c := newMock(t, func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, 403, "Forbidden: user is deactivated")
	})

	_, err := c.GetManagedBotToken(context.Background(), 111)
	if !IsBotDeactivated(err) {
		t.Errorf("want IsBotDeactivated, got %v", err)
	}
}

func TestGetManagedBotToken_Forbidden_NotDeactivated(t *testing.T) {
	// 403 with a different description must NOT map to ErrBotDeactivated.
	c := newMock(t, func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, 403, "Forbidden: bot was blocked by the user")
	})

	_, err := c.GetManagedBotToken(context.Background(), 111)
	if IsBotDeactivated(err) {
		t.Errorf("unexpected deactivated mapping for bot-blocked 403")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 403 {
		t.Errorf("expected bare APIError{403}, got %v", err)
	}
}

// ---------- ReplaceManagedBotToken ----------

func TestReplaceManagedBotToken_Success(t *testing.T) {
	c := newMock(t, expectMethod(t, http.MethodPost, "replaceManagedBotToken", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UserID int64 `json:"user_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.UserID != 42 {
			t.Errorf("user_id = %d", req.UserID)
		}
		writeOK(w, `"42:NEWTOKENVALUE"`)
	}))

	tok, err := c.ReplaceManagedBotToken(context.Background(), 42)
	if err != nil {
		t.Fatalf("ReplaceManagedBotToken: %v", err)
	}
	if tok != "42:NEWTOKENVALUE" {
		t.Errorf("token = %q", tok)
	}
}

// ---------- GetWebhookInfo ----------

func TestGetWebhookInfo_Success(t *testing.T) {
	c := newMock(t, expectMethod(t, http.MethodPost, "getWebhookInfo", func(w http.ResponseWriter, _ *http.Request) {
		writeOK(w, `{
			"url":"https://example.com/webhook",
			"has_custom_certificate":false,
			"pending_update_count":3,
			"allowed_updates":["message","managed_bot"]
		}`)
	}))

	info, err := c.GetWebhookInfo(context.Background())
	if err != nil {
		t.Fatalf("GetWebhookInfo: %v", err)
	}
	if info.URL != "https://example.com/webhook" {
		t.Errorf("URL = %q", info.URL)
	}
	if info.PendingUpdateCount != 3 {
		t.Errorf("PendingUpdateCount = %d", info.PendingUpdateCount)
	}
	if len(info.AllowedUpdates) != 2 || info.AllowedUpdates[1] != "managed_bot" {
		t.Errorf("AllowedUpdates = %v", info.AllowedUpdates)
	}
}

func TestGetWebhookInfo_Empty(t *testing.T) {
	// No webhook set: URL is "" and PendingUpdateCount may be 0.
	c := newMock(t, func(w http.ResponseWriter, _ *http.Request) {
		writeOK(w, `{"url":"","has_custom_certificate":false,"pending_update_count":0}`)
	})

	info, err := c.GetWebhookInfo(context.Background())
	if err != nil {
		t.Fatalf("GetWebhookInfo: %v", err)
	}
	if info.URL != "" {
		t.Errorf("URL = %q, want empty", info.URL)
	}
}

// ---------- SendMessage ----------

func TestSendMessage_Success(t *testing.T) {
	c := newMock(t, expectMethod(t, http.MethodPost, "sendMessage", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		if req.ChatID != 500 || req.Text != "hi" {
			t.Errorf("req = %+v", req)
		}
		// Telegram returns the sent message as result; we ignore it here.
		writeOK(w, `{"message_id":1,"date":0,"chat":{"id":500,"type":"private"}}`)
	}))

	if err := c.SendMessage(context.Background(), 500, "hi"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
}

// ---------- SetWebhook ----------

func TestSetWebhook_Success(t *testing.T) {
	c := newMock(t, expectMethod(t, http.MethodPost, "setWebhook", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL            string   `json:"url"`
			AllowedUpdates []string `json:"allowed_updates,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		if req.URL != "https://example.com/hook" {
			t.Errorf("url = %q", req.URL)
		}
		if len(req.AllowedUpdates) != 1 || req.AllowedUpdates[0] != "message" {
			t.Errorf("allowed_updates = %v", req.AllowedUpdates)
		}
		writeOK(w, `true`)
	}))

	err := c.SetWebhook(context.Background(), "https://example.com/hook", []string{"message"})
	if err != nil {
		t.Fatalf("SetWebhook: %v", err)
	}
}

func TestSetWebhook_NilAllowedUpdates(t *testing.T) {
	// Nil allowedUpdates should be omitted from the JSON body so
	// Telegram applies its default (all types except chat_member).
	c := newMock(t, expectMethod(t, http.MethodPost, "setWebhook", func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("decode: %v", err)
		}
		if _, present := raw["allowed_updates"]; present {
			t.Errorf("allowed_updates should be omitted when nil, body = %v", raw)
		}
		writeOK(w, `true`)
	}))

	if err := c.SetWebhook(context.Background(), "https://example.com/hook", nil); err != nil {
		t.Fatalf("SetWebhook: %v", err)
	}
}

func TestSetWebhook_RejectsEmptyURL(t *testing.T) {
	// No HTTP call should be made — validation is client-side.
	c := newMock(t, func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("should not reach server; got request for %q", r.URL.Path)
	})

	err := c.SetWebhook(context.Background(), "", nil)
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error should mention 'required': %v", err)
	}
}

func TestSetWebhook_RejectsHTTP(t *testing.T) {
	// Telegram rejects plain HTTP with 400; catch client-side.
	c := newMock(t, func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("should not reach server; got request for %q", r.URL.Path)
	})

	err := c.SetWebhook(context.Background(), "http://example.com/hook", nil)
	if err == nil {
		t.Fatal("expected error for plain-HTTP URL")
	}
	if !strings.Contains(err.Error(), "https://") {
		t.Errorf("error should mention https://: %v", err)
	}
}

func TestSetWebhook_ServerRejects(t *testing.T) {
	// Server-side error (e.g., invalid cert) should surface as APIError.
	c := newMock(t, func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, 400, "Bad Request: webhook URL not valid")
	})

	err := c.SetWebhook(context.Background(), "https://valid-looking.example/hook", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 400 {
		t.Errorf("want APIError{400}, got %v", err)
	}
}

func TestSendMessage_ChatNotFound(t *testing.T) {
	c := newMock(t, func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, 400, "Bad Request: chat not found")
	})

	err := c.SendMessage(context.Background(), 999, "hi")
	// No sentinel for 400; expect bare APIError.
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if IsUnauthorized(err) || IsBotDeactivated(err) || IsTooManyRequests(err) {
		t.Errorf("no sentinel should match 400: %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 400 {
		t.Errorf("want APIError{400}, got %v", err)
	}
}

// ---------- do() transport-level errors ----------

func TestDo_RateLimited(t *testing.T) {
	c := newMock(t, func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, 429, "Too Many Requests: retry after 5")
	})

	_, err := c.GetMe(context.Background())
	if !IsTooManyRequests(err) {
		t.Errorf("want IsTooManyRequests, got %v", err)
	}
}

func TestDo_NetworkError(t *testing.T) {
	c := NewClient("tok", WithBaseURL("http://127.0.0.1:1"))
	_, err := c.GetMe(context.Background())
	if err == nil {
		t.Fatal("want error for unreachable endpoint")
	}
	if !strings.Contains(err.Error(), "tgapi getMe") {
		t.Errorf("error should name the method: %v", err)
	}
}

func TestDo_MalformedResponse(t *testing.T) {
	c := newMock(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "not json at all")
	})

	_, err := c.GetMe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "decode envelope") {
		t.Errorf("want envelope decode error, got %v", err)
	}
}

func TestDo_ContextCancelled(t *testing.T) {
	c := newMock(t, func(w http.ResponseWriter, _ *http.Request) {
		writeOK(w, `{"id":1,"is_bot":true,"first_name":"x"}`)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	_, err := c.GetMe(ctx)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
}

func TestDo_SuccessNoResult(t *testing.T) {
	// Some methods (SendMessage) do not decode a result into the caller.
	// Verify SendMessage discards the server's result without erroring.
	c := newMock(t, func(w http.ResponseWriter, _ *http.Request) {
		writeOK(w, `{"message_id":1,"date":0}`)
	})

	if err := c.SendMessage(context.Background(), 1, "hi"); err != nil {
		t.Errorf("SendMessage with ignored result: %v", err)
	}
}
