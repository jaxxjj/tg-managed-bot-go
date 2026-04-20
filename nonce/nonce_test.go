package nonce

import (
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
	username := strings.ToUpper("alva_" + n + "_Bot") // weird casing
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
		"alva_tooshort_bot",          // nonce too short
		"wrong_h3k9mpq4x7nv_bot",     // wrong prefix
		"alva_h3k9mpq4x7nv",          // no _bot suffix
		"alva_H3K9MPQ4X7NV_bot_xxx",  // trailing garbage
		"",                           // empty
		"alva__bot",                  // empty nonce
		"alva_h3k9mpq-x7nv_bot",      // invalid char
		"alva_lllllllllll_bot",       // l not in alphabet (ambiguous with 1)
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
		if err := Validate(s); err == nil {
			t.Errorf("Validate(%q) should fail", s)
		}
	}
}
