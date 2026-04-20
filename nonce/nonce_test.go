package nonce

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestNew_DefaultLength(t *testing.T) {
	n, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(n) != DefaultLength {
		t.Errorf("len = %d, want %d", len(n), DefaultLength)
	}
	if err := Validate(n); err != nil {
		t.Errorf("generated nonce fails Validate: %v", err)
	}
}

func TestNewN_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		n, err := NewN(12)
		if err != nil {
			t.Fatalf("NewN: %v", err)
		}
		if seen[n] {
			t.Fatalf("collision at iteration %d: %q", i, n)
		}
		seen[n] = true
	}
}

func TestNewN_RejectsShort(t *testing.T) {
	_, err := NewN(4)
	if err == nil {
		t.Errorf("expected error for n=4")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("error does not wrap ErrInvalid: %v", err)
	}
}

func TestNewN_UsesAlphabet(t *testing.T) {
	for i := 0; i < 100; i++ {
		n, err := NewN(16)
		if err != nil {
			t.Fatalf("NewN: %v", err)
		}
		for j, r := range n {
			if !strings.ContainsRune(alphabet, r) {
				t.Errorf("iter %d char %d = %q not in alphabet", i, j, r)
			}
		}
	}
}

func TestExtract_Roundtrip(t *testing.T) {
	n, _ := New()
	username := "alva_" + n + "_bot"
	got, ok := Extract(username, "alva")
	if !ok {
		t.Fatalf("Extract failed for %q", username)
	}
	if got != n {
		t.Errorf("got %q, want %q", got, n)
	}
}

func TestExtract_CaseInsensitive(t *testing.T) {
	n, _ := New()
	username := strings.ToUpper("alva_" + n + "_Bot")
	got, ok := Extract(username, "alva")
	if !ok {
		t.Fatalf("Extract failed for %q", username)
	}
	if got != n {
		t.Errorf("got %q, want %q", got, n)
	}
}

func TestExtract_MissFields(t *testing.T) {
	cases := []string{
		"alva_tooshort_bot",         // nonce too short
		"wrong_h3k9mpq4x7nv_bot",    // wrong prefix
		"alva_h3k9mpq4x7nv",         // no _bot suffix
		"alva_h3k9mpq4x7nv_bot_xxx", // trailing garbage
		"",                          // empty
		"alva__bot",                 // empty nonce
		"alva_h3k9mpq-x7nv_bot",     // invalid char
		"alva_lllllllllll_bot",      // l not in alphabet (ambiguous with 1)
	}
	for _, c := range cases {
		if _, ok := Extract(c, "alva"); ok {
			t.Errorf("expected miss for %q", c)
		}
	}
}

func TestPattern(t *testing.T) {
	p := Pattern("alva", 12)
	re := regexp.MustCompile(p)
	if !re.MatchString("alva_h3k9mpq4x7nv_bot") {
		t.Errorf("pattern %q does not match valid input", p)
	}
	if re.MatchString("alva_lllllllllll_bot") {
		t.Errorf("pattern %q should not match l-only (ambiguous alphabet)", p)
	}
}

// TestPattern_DerivedFromAlphabet ensures the Pattern regex accepts exactly
// the characters in alphabet and rejects every other Latin letter/digit.
// This is what catches silent drift if alphabet or Pattern is changed.
func TestPattern_DerivedFromAlphabet(t *testing.T) {
	p := Pattern("alva", MinLength)
	re := regexp.MustCompile(p)

	for _, r := range "0123456789abcdefghijklmnopqrstuvwxyz" {
		nonce := strings.Repeat(string(r), MinLength)
		username := "alva_" + nonce + "_bot"
		want := strings.ContainsRune(alphabet, r)
		got := re.MatchString(username)
		if got != want {
			t.Errorf("char %q: alphabet=%v, pattern matches=%v (drift detected)", r, want, got)
		}
	}
}

func TestValidate(t *testing.T) {
	valid, _ := New()
	if err := Validate(valid); err != nil {
		t.Errorf("Validate(%q): %v", valid, err)
	}
	invalid := []string{
		"",
		"short",
		"h3k9mpq4x7nL", // L not in alphabet
		"h3k9 pq4x7nv", // space
	}
	for _, s := range invalid {
		err := Validate(s)
		if err == nil {
			t.Errorf("Validate(%q) should fail", s)
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("Validate(%q) error does not wrap ErrInvalid: %v", s, err)
		}
	}
}

func TestPackIntoUsername_Roundtrip(t *testing.T) {
	for i := 0; i < 100; i++ {
		n, err := New()
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		username, err := PackIntoUsername("alva", n)
		if err != nil {
			t.Fatalf("PackIntoUsername: %v", err)
		}
		got, ok := Extract(username, "alva")
		if !ok {
			t.Fatalf("Extract failed for %q", username)
		}
		if got != n {
			t.Errorf("roundtrip: got %q, want %q", got, n)
		}
	}
}

func TestPackIntoUsername_CaseInsensitivePrefix(t *testing.T) {
	// Case folding is applied (ALVA → alva). Whitespace is NOT stripped
	// (see TestPackIntoUsername_Errors for the whitespace case).
	n, _ := New()
	u1, _ := PackIntoUsername("alva", n)
	u2, _ := PackIntoUsername("ALVA", n)
	if u1 != u2 {
		t.Errorf("prefix case normalization inconsistent: %q / %q", u1, u2)
	}
}

func TestPackIntoUsername_Errors(t *testing.T) {
	n, _ := New()
	cases := []struct {
		name   string
		prefix string
		nonce  string
	}{
		{"empty prefix", "", n},
		{"bad prefix char", "alva!", n},
		{"short nonce", "alva", "abc"},
		{"too long packed", strings.Repeat("x", 20), n}, // 20 + 1 + 12 + 4 = 37 > 32

		// Telegram bot usernames must start with a letter.
		{"prefix starts with digit", "9alva", n},
		{"prefix starts with underscore", "_alva", n},

		// PackIntoUsername must NOT silently trim whitespace — Extract
		// would later miss, breaking the pack/extract symmetry.
		{"prefix leading space", " alva", n},
		{"prefix trailing space", "alva ", n},
		{"prefix with inner space", "al va", n},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := PackIntoUsername(c.prefix, c.nonce)
			if err == nil {
				t.Errorf("expected error")
			}
		})
	}
}

// TestPackIntoUsername_SymmetricWithExtract is the contract test: any
// username produced by PackIntoUsername must be recoverable by Extract
// with the same prefix. If this ever fails, the two have drifted.
func TestPackIntoUsername_SymmetricWithExtract(t *testing.T) {
	for _, prefix := range []string{"alva", "ALVA", "test_rig", "a", "my1_app_2"} {
		n, err := New()
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		username, err := PackIntoUsername(prefix, n)
		if err != nil {
			t.Logf("skipping prefix %q (rejected): %v", prefix, err)
			continue
		}
		// Extract should succeed with both the original and the normalized
		// (lower-cased) prefix. It must NOT require a trim.
		got, ok := Extract(username, prefix)
		if !ok {
			t.Errorf("Extract(%q, %q) miss", username, prefix)
			continue
		}
		if got != n {
			t.Errorf("prefix=%q roundtrip mismatch: got %q, want %q", prefix, got, n)
		}
	}
}

func BenchmarkNew(b *testing.B) {
	for b.Loop() {
		_, _ = New()
	}
}

func BenchmarkExtract_Hit(b *testing.B) {
	n, _ := New()
	username := "alva_" + n + "_bot"
	b.ResetTimer()
	for b.Loop() {
		_, _ = Extract(username, "alva")
	}
}

func BenchmarkExtract_Miss(b *testing.B) {
	username := "something_else_entirely"
	for b.Loop() {
		_, _ = Extract(username, "alva")
	}
}

func BenchmarkPackIntoUsername(b *testing.B) {
	n, _ := New()
	b.ResetTimer()
	for b.Loop() {
		_, _ = PackIntoUsername("alva", n)
	}
}
