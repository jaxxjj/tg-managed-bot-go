package link

import (
	"fmt"
	"net/url"
	"strings"
)

// NameMaxLen is the Telegram-enforced max length for a bot display name.
const NameMaxLen = 64

// Options controls the deep link generation.
//
// Only ManagerBotUsername and SuggestedUsername are required. If
// SuggestedName is non-empty, it is appended as a `?name=` query parameter.
type Options struct {
	// ManagerBotUsername is the manager bot's @-username, without the '@'.
	// The manager bot must have "Bot Management Mode" enabled in BotFather.
	ManagerBotUsername string

	// SuggestedUsername is the username pre-filled in Telegram's Create Bot
	// dialog. The user can edit it before confirming. Must satisfy
	// Telegram's username rules; see [ValidateUsername].
	SuggestedUsername string

	// SuggestedName is the display name pre-filled in the dialog.
	// Optional; 1–64 chars if set. The user can edit it.
	SuggestedName string
}

// BuildNewBot constructs the Managed Bots creation deep link of the form
//
//	https://t.me/newbot/{manager_bot}/{suggested_username}?name={suggested_name}
//
// Returns an error if required fields are missing or invalid.
//
// Reference: https://core.telegram.org/bots/features#managed-bots
func BuildNewBot(opts Options) (string, error) {
	if strings.TrimSpace(opts.ManagerBotUsername) == "" {
		return "", fmt.Errorf("link: ManagerBotUsername is required")
	}
	if err := ValidateUsername(opts.ManagerBotUsername); err != nil {
		return "", fmt.Errorf("link: invalid manager username: %w", err)
	}
	if err := ValidateUsername(opts.SuggestedUsername); err != nil {
		return "", fmt.Errorf("link: invalid suggested username: %w", err)
	}

	u := &url.URL{
		Scheme: "https",
		Host:   "t.me",
		Path:   "/newbot/" + opts.ManagerBotUsername + "/" + opts.SuggestedUsername,
	}

	if opts.SuggestedName != "" {
		name := strings.TrimSpace(opts.SuggestedName)
		if n := len([]rune(name)); n > NameMaxLen {
			return "", fmt.Errorf("link: name length %d exceeds max %d", n, NameMaxLen)
		}
		q := u.Query()
		q.Set("name", name)
		u.RawQuery = q.Encode()
	}

	return u.String(), nil
}
