// Package link builds Telegram Managed Bot creation deep links and
// validates/sanitizes the suggested bot usernames embedded in them.
//
// Telegram bot usernames have the following rules:
//   - 5 to 32 characters
//   - Latin letters (a-z, A-Z), digits (0-9), and underscores only
//   - Must end with "bot" or "_bot" (case insensitive)
//   - Must start with a letter
//
// See [Bot FAQ] for canonical rules.
//
// [Bot FAQ]: https://core.telegram.org/bots/faq
package link

import (
	"fmt"
	"regexp"
	"strings"
)

// UsernameMinLen and UsernameMaxLen are the Telegram-enforced bounds on
// bot usernames.
const (
	UsernameMinLen = 5
	UsernameMaxLen = 32
)

var validUsernameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*[bB][oO][tT]$`)

// ValidateUsername reports an error if username is not a valid Telegram
// bot username. It does not check global uniqueness (that is enforced by
// Telegram at creation time).
func ValidateUsername(username string) error {
	if n := len(username); n < UsernameMinLen || n > UsernameMaxLen {
		return fmt.Errorf("link: username length %d not in [%d, %d]", n, UsernameMinLen, UsernameMaxLen)
	}
	if !validUsernameRe.MatchString(username) {
		return fmt.Errorf("link: username %q does not match rules (must start with letter, end with 'bot', only [a-zA-Z0-9_])", username)
	}
	return nil
}

// SanitizeUsername attempts to coerce a raw string into a valid bot username:
//   - lowercases
//   - replaces any non-[a-z0-9_] with "_"
//   - ensures it starts with a letter (prepends "alva_" if not)
//   - ensures it ends with "_bot" (appends if not)
//   - truncates to UsernameMaxLen
//
// Returns an error only if the input is empty after cleaning.
func SanitizeUsername(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", fmt.Errorf("link: empty username")
	}

	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s = b.String()

	// Ensure starts with a letter.
	if s == "" || !(s[0] >= 'a' && s[0] <= 'z') {
		s = "a" + s
	}

	// Ensure ends with "bot" (bare trailing "bot" is acceptable per TG rules).
	if !strings.HasSuffix(s, "bot") {
		if !strings.HasSuffix(s, "_") {
			s += "_"
		}
		s += "bot"
	}

	// Truncate from the left up to the minimum length if too long, preserving the
	// "_bot" suffix.
	if len(s) > UsernameMaxLen {
		suffix := "_bot"
		if strings.HasSuffix(s, suffix) {
			head := s[:len(s)-len(suffix)]
			if len(head)+len(suffix) > UsernameMaxLen {
				head = head[:UsernameMaxLen-len(suffix)]
			}
			s = head + suffix
		} else {
			s = s[:UsernameMaxLen]
		}
	}

	if err := ValidateUsername(s); err != nil {
		return "", fmt.Errorf("link: could not sanitize %q: %w", raw, err)
	}
	return s, nil
}
