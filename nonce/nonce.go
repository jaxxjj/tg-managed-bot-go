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
//
// Invariant: len(alphabet) must be a power of two so NewN can use a bitmask
// instead of modulo and avoid bias. Asserted at init time.
const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// alphabetMask is len(alphabet)-1. Usable only because len(alphabet) is 32.
const alphabetMask = 0x1F

// alphabetClass is the regex character-class form of alphabet, derived at
// init so [Pattern] never drifts from the actual alphabet. Characters in
// the alphabet have no regex-special meaning inside a character class,
// but regexp.QuoteMeta is applied for belt-and-suspenders safety.
var alphabetClass = "[" + regexp.QuoteMeta(alphabet) + "]"

func init() {
	// Enforce the power-of-two invariant at program startup.
	if n := len(alphabet); n&(n-1) != 0 {
		panic(fmt.Sprintf("nonce: alphabet length %d is not a power of two", n))
	}
	if len(alphabet) != alphabetMask+1 {
		panic("nonce: alphabetMask does not match alphabet length")
	}
}

// DefaultLength is the default nonce length. 12 chars of 32-symbol alphabet
// ≈ 60 bits of entropy — enough to prevent online guessing while keeping
// the generated username short (e.g. "alva_h3k9mpq4x7nv_bot").
const DefaultLength = 12

// MinLength guards against misuse. Values below 8 are rejected.
const MinLength = 8

// ErrInvalid is the sentinel for validation failures from this package.
// Callers can use errors.Is(err, ErrInvalid) to detect.
var ErrInvalid = errors.New("nonce: invalid")

// New returns a new random nonce of DefaultLength characters.
func New() (string, error) {
	return NewN(DefaultLength)
}

// NewN returns a random nonce of n characters using the Crockford base32
// alphabet. n must be >= MinLength.
func NewN(n int) (string, error) {
	if n < MinLength {
		return "", fmt.Errorf("%w: length %d below minimum %d", ErrInvalid, n, MinLength)
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("nonce: read random: %w", err)
	}
	out := make([]byte, n)
	for i, b := range buf {
		// alphabet length is a power of two (enforced at init), so a
		// bitmask is unbiased. Using modulo would also work here but
		// the bitmask is more explicit about the invariant.
		out[i] = alphabet[b&alphabetMask]
	}
	return string(out), nil
}

// Pattern returns the regex pattern used to extract a nonce embedded in a
// bot username of the form "<prefix>_<nonce>_bot". The prefix and length
// are escaped/interpolated into the pattern.
//
// The character class is derived from the alphabet constant at init time;
// it cannot drift.
//
// Example: Pattern("alva", 12) returns "^alva_([0123456789abcdefghjkmnpqrstvwxyz]{12})_bot$"
func Pattern(prefix string, length int) string {
	return fmt.Sprintf(`^%s_(%s{%d})_bot$`, regexp.QuoteMeta(strings.ToLower(prefix)), alphabetClass, length)
}

// PackIntoUsername builds a child-bot username of the form
// "<prefix>_<nonce>_bot". Pairs with [Extract]:
//
//	got, ok := Extract(PackIntoUsername(p, n)); got == n && ok
//
// The prefix is lower-cased (matching Extract/Pattern's case handling)
// but NOT trimmed of whitespace — [Extract] does not trim either, so
// trimming here would produce asymmetric normalization (Pack would
// succeed on "  alva  " but Extract would miss on the same prefix).
// Leading/trailing whitespace in prefix is rejected via the per-character
// check below.
//
// Returns an error if:
//   - prefix is empty, does not start with [a-z] (Telegram bot usernames
//     must begin with a letter), or contains non-[a-z0-9_] characters;
//   - n is not a valid nonce;
//   - the resulting username would exceed the Telegram max bot-username
//     length (32 chars).
func PackIntoUsername(prefix, n string) (string, error) {
	prefix = strings.ToLower(prefix)
	if prefix == "" {
		return "", fmt.Errorf("%w: empty prefix", ErrInvalid)
	}
	// Telegram bot usernames must start with a letter. Enforce at the
	// prefix level so PackIntoUsername is a safe constructor (rather
	// than deferring the failure to BuildNewBot or Telegram itself).
	if prefix[0] < 'a' || prefix[0] > 'z' {
		return "", fmt.Errorf("%w: prefix must start with a letter [a-z] (got %q)", ErrInvalid, prefix[0])
	}
	for i, r := range prefix {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return "", fmt.Errorf("%w: prefix character %q at index %d not in [a-z0-9_]", ErrInvalid, r, i)
		}
	}
	if err := Validate(n); err != nil {
		return "", err
	}
	username := prefix + "_" + n + "_bot"
	// Telegram caps bot usernames at 32 characters; enforce here so the
	// caller gets a clear error rather than a downstream deep-link failure.
	if len(username) > 32 {
		return "", fmt.Errorf("%w: packed username length %d exceeds Telegram max 32 (prefix=%q, nonce=%q)",
			ErrInvalid, len(username), prefix, n)
	}
	// Sanity: roundtrip through Extract to catch any pack/extract drift.
	got, ok := ExtractN(username, prefix, len(n))
	if !ok || got != n {
		return "", fmt.Errorf("%w: pack/extract roundtrip failed (prefix=%q, nonce=%q, username=%q)",
			ErrInvalid, prefix, n, username)
	}
	return username, nil
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
// On failure the returned error wraps [ErrInvalid].
func Validate(s string) error {
	if len(s) < MinLength {
		return fmt.Errorf("%w: length %d below minimum %d", ErrInvalid, len(s), MinLength)
	}
	for i, r := range s {
		if !strings.ContainsRune(alphabet, r) {
			return fmt.Errorf("%w: invalid character %q at index %d", ErrInvalid, r, i)
		}
	}
	return nil
}
