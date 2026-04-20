package tgapi

import (
	"errors"
	"fmt"
	"strings"
)

// APIError is the structured error the Telegram Bot API returns for
// non-OK responses. Every failed call surfaces an APIError either
// directly or wrapped by a sentinel (see below).
//
// Reference: https://core.telegram.org/bots/api#making-requests
type APIError struct {
	// Code is the HTTP-equivalent status (401, 403, 429, ...).
	Code int

	// Description is the human-readable message from Telegram. The
	// string format is not part of the API contract; prefer the
	// sentinel checks (see [IsUnauthorized], [IsBotDeactivated],
	// [IsTooManyRequests]) for behavioural branching.
	Description string
}

// Error returns "tgapi: <code> <description>".
func (e *APIError) Error() string {
	return fmt.Sprintf("tgapi: %d %s", e.Code, e.Description)
}

// Sentinel errors returned (wrapped) by [Client] methods. Use
// [errors.Is] or the Is* helpers to test for them.
//
// Values are wrapped via fmt.Errorf("%w: ...", sentinel, ...) so that
// callers still get the descriptive context, yet errors.Is keeps working.
var (
	// ErrUnauthorized indicates an invalid or revoked bot token.
	// HTTP 401. Typical causes: the user ran /token in @BotFather,
	// the token was rotated via ReplaceManagedBotToken elsewhere,
	// or the token was never valid.
	ErrUnauthorized = errors.New("tgapi: unauthorized")

	// ErrBotDeactivated indicates the bot was deleted by the user via
	// @BotFather. Detected from HTTP 403 with "user is deactivated"
	// in the description — Telegram's canonical phrase for this state.
	ErrBotDeactivated = errors.New("tgapi: bot deactivated")

	// ErrTooManyRequests indicates rate limiting. HTTP 429. Callers
	// should honor the retry_after hint (if present in the APIError
	// description) and back off accordingly.
	ErrTooManyRequests = errors.New("tgapi: too many requests")
)

// classify returns the appropriate sentinel for the given (code,
// description) pair, or nil when no sentinel matches.
//
// Called internally by [Client]; exported only via the Is* helpers.
func classify(code int, description string) error {
	switch code {
	case 401:
		return ErrUnauthorized
	case 403:
		// Telegram uses the phrase "user is deactivated" to signal
		// that a managed bot has been deleted by its owner. Other
		// 403s (e.g., "bot was kicked from the group") do not
		// imply deactivation.
		if strings.Contains(description, "user is deactivated") {
			return ErrBotDeactivated
		}
	case 429:
		return ErrTooManyRequests
	}
	return nil
}

// IsUnauthorized reports whether err is (or wraps) [ErrUnauthorized].
func IsUnauthorized(err error) bool { return errors.Is(err, ErrUnauthorized) }

// IsBotDeactivated reports whether err is (or wraps) [ErrBotDeactivated].
// True means the underlying managed bot has been deleted — callers should
// transition the associated record to a Deleted state.
func IsBotDeactivated(err error) bool { return errors.Is(err, ErrBotDeactivated) }

// IsTooManyRequests reports whether err is (or wraps) [ErrTooManyRequests].
func IsTooManyRequests(err error) bool { return errors.Is(err, ErrTooManyRequests) }
