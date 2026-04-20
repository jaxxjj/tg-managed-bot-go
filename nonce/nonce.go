// Package nonce generates and parses pairing nonces used to correlate
// a Managed Bot creation with the client session that requested it.
//
// Nonces are embedded in the suggested child-bot username so the manager
// bot can correlate a [managed_bot_created] update back to the originating
// client without a separate lookup.
//
// Alphabet choice: Crockford base32 (no I/L/O/U) for human readability and
// to avoid case-sensitivity surprises. Generated nonces are lowercase.
//
// [managed_bot_created]: https://core.telegram.org/bots/api#managedbotcreated
package nonce

import (
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// alphabet is Crockford base32, lowercased.
// Excludes i, l, o, u to avoid visual ambiguity with 1/0 and profanity.
const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// DefaultLength is the default nonce length. 12 chars of 32-symbol alphabet
// ≈ 60 bits of entropy — enough to prevent online guessing while keeping
// the generated username short (e.g. "alva_h3k9mpq4x7nv_bot").
const DefaultLength = 12

// MinLength guards against misuse. Values below 8 are rejected.
const MinLength = 8

// New returns a new random nonce of DefaultLength characters.
func New() (string, error) {
	return NewN(DefaultLength)
}

// NewN returns a random nonce of n characters using the Crockford base32
// alphabet. n must be >= MinLength.
func NewN(n int) (string, error) {
	if n < MinLength {
		return "", fmt.Errorf("nonce: length %d below minimum %d", n, MinLength)
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("nonce: read random: %w", err)
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}

// Pattern returns the regex pattern used to extract a nonce embedded in a
// bot username of the form "<prefix>_<nonce>_bot". The prefix and length
// are escaped into the pattern.
//
// Example: Pattern("alva", 12) returns "^alva_([0-9a-hjkmnp-tv-z]{12})_bot$"
func Pattern(prefix string, length int) string {
	return fmt.Sprintf(`^%s_([0-9a-hjkmnp-tv-z]{%d})_bot$`, regexp.QuoteMeta(strings.ToLower(prefix)), length)
}

// Extract pulls the nonce out of a child bot username if it matches the
// expected pattern. Returns ok=false if the username does not match
// (e.g. the user edited the suggested username during the Create dialog).
//
// Comparison is case-insensitive; usernames from Telegram may vary in case.
func Extract(username, prefix string) (string, bool) {
	return ExtractN(username, prefix, DefaultLength)
}

// ExtractN is like [Extract] but with an explicit nonce length.
func ExtractN(username, prefix string, length int) (string, bool) {
	if length < MinLength {
		return "", false
	}
	re, err := regexp.Compile(Pattern(prefix, length))
	if err != nil {
		return "", false
	}
	m := re.FindStringSubmatch(strings.ToLower(username))
	if len(m) != 2 {
		return "", false
	}
	return m[1], true
}

// Validate reports whether s is a structurally valid nonce (correct length,
// correct alphabet). Does not check that the nonce was actually issued.
func Validate(s string) error {
	if len(s) < MinLength {
		return fmt.Errorf("nonce: length %d below minimum %d", len(s), MinLength)
	}
	for i, r := range s {
		if !strings.ContainsRune(alphabet, r) {
			return fmt.Errorf("nonce: invalid character %q at index %d", r, i)
		}
	}
	return nil
}

// ErrInvalid is returned by callers that want a sentinel error for
// failed Validate.
var ErrInvalid = errors.New("nonce: invalid")
