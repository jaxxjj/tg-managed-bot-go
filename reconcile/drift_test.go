package reconcile

import (
	"errors"
	"fmt"
	"testing"

	"github.com/alva-ai/tg-managed-bot-go/tgapi"
)

// driftKinds returns the Kind field of every drift in the given slice,
// preserving order. Helper for concise table assertions.
func driftKinds(ds []Drift) []DriftKind {
	out := make([]DriftKind, len(ds))
	for i, d := range ds {
		out[i] = d.Kind
	}
	return out
}

func equalKinds(a, b []DriftKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDriftKind_String(t *testing.T) {
	cases := []struct {
		k    DriftKind
		want string
	}{
		{DriftNone, "none"},
		{DriftDeleted, "deleted"},
		{DriftTokenRotated, "token_rotated"},
		{DriftUnreachable, "unreachable"},
		{DriftWebhookHijacked, "webhook_hijacked"},
		{DriftPrivacyRegression, "privacy_regression"},
		{DriftUsernameChanged, "username_changed"},
		{DriftKind(99), "drift(99)"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("%d.String() = %q, want %q", int(c.k), got, c.want)
		}
	}
}

func TestCheckDrift_HappyPath(t *testing.T) {
	// All matching → no drift.
	state := State{
		BotID:              123,
		ExpectedWebhookURL: "https://alva.example/tg/managed/123",
		ExpectPrivacyOff:   true,
		LastKnownUsername:  "alva_acme_bot",
	}
	obs := Observed{
		Me: &tgapi.User{
			ID:                      123,
			Username:                "alva_acme_bot",
			CanReadAllGroupMessages: true,
		},
		Webhook: &tgapi.WebhookInfo{
			URL: "https://alva.example/tg/managed/123",
		},
	}
	if ds := CheckDrift(state, obs); len(ds) != 0 {
		t.Errorf("expected no drift, got %+v", ds)
	}
}

func TestCheckDrift_Deleted(t *testing.T) {
	state := State{BotID: 1}
	obs := Observed{
		GetMeErr: fmt.Errorf("call: %w", tgapi.ErrBotDeactivated),
	}
	ds := CheckDrift(state, obs)
	if !equalKinds(driftKinds(ds), []DriftKind{DriftDeleted}) {
		t.Errorf("got kinds %v, want [DriftDeleted]", driftKinds(ds))
	}
}

func TestCheckDrift_TokenRotated(t *testing.T) {
	state := State{BotID: 1}
	obs := Observed{
		GetMeErr: fmt.Errorf("call: %w", tgapi.ErrUnauthorized),
	}
	ds := CheckDrift(state, obs)
	if !equalKinds(driftKinds(ds), []DriftKind{DriftTokenRotated}) {
		t.Errorf("got kinds %v, want [DriftTokenRotated]", driftKinds(ds))
	}
}

func TestCheckDrift_Deleted_ShortCircuits(t *testing.T) {
	// Even when other fields would normally drift, DriftDeleted alone is
	// emitted — it's terminal.
	state := State{
		BotID:              1,
		ExpectedWebhookURL: "https://alva/expected",
		ExpectPrivacyOff:   true,
		LastKnownUsername:  "old",
	}
	obs := Observed{
		GetMeErr: tgapi.ErrBotDeactivated,
		// Even if somehow a webhook or Me was observed, it should be
		// ignored after a deletion signal.
		Webhook: &tgapi.WebhookInfo{URL: "https://evil"},
		Me: &tgapi.User{
			Username:                "new",
			CanReadAllGroupMessages: false,
		},
	}
	ds := CheckDrift(state, obs)
	if !equalKinds(driftKinds(ds), []DriftKind{DriftDeleted}) {
		t.Errorf("deletion should short-circuit; got %v", driftKinds(ds))
	}
}

func TestCheckDrift_Unreachable_StillChecksWebhook(t *testing.T) {
	// Transport error on getMe: keep checking the webhook channel —
	// Telegram's two endpoints can fail independently.
	state := State{
		BotID:              1,
		ExpectedWebhookURL: "https://alva/expected",
	}
	obs := Observed{
		GetMeErr: errors.New("i/o timeout"),
		Webhook:  &tgapi.WebhookInfo{URL: "https://hijacker.example"},
	}
	ds := CheckDrift(state, obs)
	want := []DriftKind{DriftUnreachable, DriftWebhookHijacked}
	if !equalKinds(driftKinds(ds), want) {
		t.Errorf("got %v, want %v", driftKinds(ds), want)
	}
}

func TestCheckDrift_WebhookHijacked(t *testing.T) {
	state := State{
		BotID:              1,
		ExpectedWebhookURL: "https://alva.example/hook",
	}
	obs := Observed{
		Me: &tgapi.User{Username: "ok"},
		Webhook: &tgapi.WebhookInfo{
			URL: "https://attacker.example/hook",
		},
	}
	// LastKnownUsername unset → skip username check.
	// ExpectPrivacyOff defaults to false, matches Me.CanReadAllGroupMessages=false.
	ds := CheckDrift(state, obs)
	if !equalKinds(driftKinds(ds), []DriftKind{DriftWebhookHijacked}) {
		t.Errorf("got %v", driftKinds(ds))
	}
}

func TestCheckDrift_WebhookCheck_SkipsWhenExpectedEmpty(t *testing.T) {
	state := State{} // ExpectedWebhookURL empty
	obs := Observed{
		Me:      &tgapi.User{},
		Webhook: &tgapi.WebhookInfo{URL: "https://anything"},
	}
	if ds := CheckDrift(state, obs); len(ds) != 0 {
		t.Errorf("expected no drift when ExpectedWebhookURL empty, got %v", ds)
	}
}

func TestCheckDrift_WebhookErr_EmitsUnreachable(t *testing.T) {
	// getWebhookInfo failure is itself signal: we cannot verify the
	// webhook. Emit DriftUnreachable so callers see that the channel
	// is in an unknown state rather than silently accepting "no drift".
	state := State{ExpectedWebhookURL: "https://alva.example/hook"}
	obs := Observed{
		Me:         &tgapi.User{},
		WebhookErr: errors.New("i/o timeout"),
	}
	ds := CheckDrift(state, obs)
	if !equalKinds(driftKinds(ds), []DriftKind{DriftUnreachable}) {
		t.Errorf("got %v, want [DriftUnreachable]", driftKinds(ds))
	}
}

func TestCheckDrift_PrivacyRegression(t *testing.T) {
	state := State{
		BotID:            1,
		ExpectPrivacyOff: true, // we want privacy disabled
	}
	obs := Observed{
		Me: &tgapi.User{
			CanReadAllGroupMessages: false, // but it's been re-enabled
		},
	}
	ds := CheckDrift(state, obs)
	if !equalKinds(driftKinds(ds), []DriftKind{DriftPrivacyRegression}) {
		t.Errorf("got %v", driftKinds(ds))
	}
}

func TestCheckDrift_UsernameChanged(t *testing.T) {
	state := State{
		BotID:             1,
		LastKnownUsername: "alva_acme_bot",
	}
	obs := Observed{
		Me: &tgapi.User{Username: "alva_beta_bot"},
	}
	ds := CheckDrift(state, obs)
	if !equalKinds(driftKinds(ds), []DriftKind{DriftUsernameChanged}) {
		t.Errorf("got %v", driftKinds(ds))
	}
}

func TestCheckDrift_UsernameCheck_SkipsWhenLastKnownEmpty(t *testing.T) {
	state := State{BotID: 1}
	obs := Observed{Me: &tgapi.User{Username: "whatever"}}
	if ds := CheckDrift(state, obs); len(ds) != 0 {
		t.Errorf("expected no drift when LastKnownUsername empty, got %v", ds)
	}
}

func TestCheckDrift_MultipleDrifts(t *testing.T) {
	// All three non-terminal drifts at once, in the stable output order:
	// webhook, privacy, username.
	state := State{
		BotID:              1,
		ExpectedWebhookURL: "https://alva.example/hook",
		ExpectPrivacyOff:   true,
		LastKnownUsername:  "alva_acme_bot",
	}
	obs := Observed{
		Me: &tgapi.User{
			Username:                "alva_beta_bot",
			CanReadAllGroupMessages: false,
		},
		Webhook: &tgapi.WebhookInfo{URL: "https://hijacker.example"},
	}
	want := []DriftKind{DriftWebhookHijacked, DriftPrivacyRegression, DriftUsernameChanged}
	if got := driftKinds(CheckDrift(state, obs)); !equalKinds(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCheckDrift_DetailsAreNonEmpty(t *testing.T) {
	// Light contract: every emitted Drift should have a Detail to log.
	state := State{BotID: 1, ExpectedWebhookURL: "https://x", LastKnownUsername: "old"}
	obs := Observed{
		Me:      &tgapi.User{Username: "new"},
		Webhook: &tgapi.WebhookInfo{URL: "https://y"},
	}
	for _, d := range CheckDrift(state, obs) {
		if d.Detail == "" {
			t.Errorf("drift %v has empty Detail", d.Kind)
		}
	}
}
