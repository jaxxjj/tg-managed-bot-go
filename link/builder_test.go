package link

import (
	"strings"
	"testing"
)

func TestBuildNewBot_HappyPath(t *testing.T) {
	got, err := BuildNewBot(Options{
		ManagerBotUsername: "alva_manager_bot",
		SuggestedUsername:  "alva_abc123_bot",
		SuggestedName:      "My Alva",
	})
	if err != nil {
		t.Fatalf("BuildNewBot: %v", err)
	}
	want := "https://t.me/newbot/alva_manager_bot/alva_abc123_bot?name=My+Alva"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildNewBot_WithoutName(t *testing.T) {
	got, err := BuildNewBot(Options{
		ManagerBotUsername: "alva_manager_bot",
		SuggestedUsername:  "alva_abc123_bot",
	})
	if err != nil {
		t.Fatalf("BuildNewBot: %v", err)
	}
	if strings.Contains(got, "?") {
		t.Errorf("expected no query string, got %q", got)
	}
}

func TestBuildNewBot_NameUrlEncoded(t *testing.T) {
	got, err := BuildNewBot(Options{
		ManagerBotUsername: "alva_manager_bot",
		SuggestedUsername:  "alva_abc123_bot",
		SuggestedName:      "My Alva & Friends",
	})
	if err != nil {
		t.Fatalf("BuildNewBot: %v", err)
	}
	if !strings.Contains(got, "My+Alva+%26+Friends") {
		t.Errorf("expected URL-encoded ampersand, got %q", got)
	}
}

func TestBuildNewBot_Errors(t *testing.T) {
	cases := []struct {
		name string
		opts Options
	}{
		{"empty manager", Options{SuggestedUsername: "alva_a_bot"}},
		{"invalid manager", Options{ManagerBotUsername: "not-valid!", SuggestedUsername: "alva_a_bot"}},
		{"invalid suggested", Options{ManagerBotUsername: "alva_manager_bot", SuggestedUsername: "x"}},
		{"name too long", Options{
			ManagerBotUsername: "alva_manager_bot",
			SuggestedUsername:  "alva_abc_bot",
			SuggestedName:      strings.Repeat("x", NameMaxLen+1),
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := BuildNewBot(c.opts); err == nil {
				t.Errorf("expected error for %+v", c.opts)
			}
		})
	}
}

func TestValidateUsername(t *testing.T) {
	good := []string{
		"a_bot", // min length 5
		"mybot",
		"Alva_Test_1_Bot",
		"a1_" + strings.Repeat("x", UsernameMaxLen-3-3) + "bot", // exactly 32 chars
	}
	for _, u := range good {
		if err := ValidateUsername(u); err != nil {
			t.Errorf("ValidateUsername(%q) rejected: %v", u, err)
		}
	}

	bad := []string{
		"",
		"bot",                  // too short
		"1startsWithDigit_bot", // starts with digit
		"has-dash_bot",         // dash not allowed
		"no_suffix",            // doesn't end in bot
		strings.Repeat("a", UsernameMaxLen) + "_bot", // too long
	}
	for _, u := range bad {
		if err := ValidateUsername(u); err == nil {
			t.Errorf("ValidateUsername(%q) accepted, should fail", u)
		}
	}
}

func TestSanitizeUsername(t *testing.T) {
	// Don't pin exact output; just require ValidateUsername to accept it
	// and log for transparency.
	cases := []string{
		"My Alva Bot",
		"alva-123",
		"123numbers",
		"  spaces  ",
	}
	for _, raw := range cases {
		got, err := SanitizeUsername(raw)
		if err != nil {
			t.Errorf("SanitizeUsername(%q): %v", raw, err)
			continue
		}
		if err := ValidateUsername(got); err != nil {
			t.Errorf("SanitizeUsername(%q) = %q, invalid: %v", raw, got, err)
		}
		t.Logf("%q → %q", raw, got)
	}

	if _, err := SanitizeUsername(""); err == nil {
		t.Error("expected error for empty input")
	}
}

// Regression test for the earlier truncation bug: a very long input that
// ends with a bare "bot" (no underscore before it) must still produce a
// valid username that ends in "bot".
func TestSanitizeUsername_TruncationPreservesBotSuffix(t *testing.T) {
	cases := []string{
		// Ends with bare "bot" after sanitize.
		strings.Repeat("x", 40) + "bot",
		// Ends with "_bot" after sanitize.
		strings.Repeat("x", 40) + "_bot",
		// Contains separators; after sanitize still ends with bot.
		strings.Repeat("My Alva ", 10),
	}
	for _, raw := range cases {
		got, err := SanitizeUsername(raw)
		if err != nil {
			t.Errorf("SanitizeUsername(%q): %v", raw, err)
			continue
		}
		if len(got) > UsernameMaxLen {
			t.Errorf("SanitizeUsername(%q) = %q (len=%d > %d)", raw, got, len(got), UsernameMaxLen)
		}
		if err := ValidateUsername(got); err != nil {
			t.Errorf("SanitizeUsername(%q) = %q, invalid: %v", raw, got, err)
		}
		lower := strings.ToLower(got)
		if !strings.HasSuffix(lower, "bot") {
			t.Errorf("SanitizeUsername(%q) = %q, does not end with 'bot'", raw, got)
		}
		t.Logf("%q → %q", raw, got)
	}
}
