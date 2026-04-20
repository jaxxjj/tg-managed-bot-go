// Package tgmanagedbot_test exercises the three packages end-to-end to
// catch any drift between nonce format, link construction, and pairing
// protocol. This test runs against the in-memory pairing store; a real
// deployment would use Redis or similar.
package tgmanagedbot_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alva-ai/tg-managed-bot-go/link"
	"github.com/alva-ai/tg-managed-bot-go/nonce"
	"github.com/alva-ai/tg-managed-bot-go/pairing"
)

// TestEndToEnd_HappyPath runs the full pairing sequence:
//
//  1. Client: generate nonce → register in store → build deep link.
//  2. Manager bot (simulated): receive managed_bot_created →
//     extract nonce → fetch token → complete pairing.
//  3. Client: poll until Ready → receive token → second poll miss.
func TestEndToEnd_HappyPath(t *testing.T) {
	const managerBot = "alva_manager_bot"
	const prefix = "alva"

	ctx := context.Background()
	store := pairing.NewMemoryStore()

	// Step 1 — client side.
	n, err := nonce.New()
	if err != nil {
		t.Fatalf("nonce.New: %v", err)
	}
	if err := store.Put(ctx, n, 15*time.Minute); err != nil {
		t.Fatalf("store.Put: %v", err)
	}
	childUsername, err := nonce.PackIntoUsername(prefix, n)
	if err != nil {
		t.Fatalf("nonce.PackIntoUsername: %v", err)
	}
	deepLink, err := link.BuildNewBot(link.Options{
		ManagerBotUsername: managerBot,
		SuggestedUsername:  childUsername,
		SuggestedName:      "My Alva",
	})
	if err != nil {
		t.Fatalf("link.BuildNewBot: %v", err)
	}
	if !strings.HasPrefix(deepLink, "https://t.me/newbot/"+managerBot+"/") {
		t.Errorf("unexpected deep link: %q", deepLink)
	}

	// Step 2 — simulated manager bot receiving the creation event.
	// Telegram may deliver the username with altered case; we simulate that.
	received := strings.ToUpper(childUsername)
	extracted, ok := nonce.Extract(received, prefix)
	if !ok {
		t.Fatalf("nonce.Extract failed for %q", received)
	}
	if extracted != n {
		t.Errorf("extracted nonce mismatch: got %q, want %q", extracted, n)
	}

	const fakeToken = "999888777:AAFakeBotTokenForTesting_xyz123"
	if err := store.Complete(ctx, extracted, fakeToken, strings.ToLower(received)); err != nil {
		t.Fatalf("store.Complete: %v", err)
	}

	// Step 3 — client poll.
	entry, err := store.FetchAndDelete(ctx, n)
	if err != nil {
		t.Fatalf("store.FetchAndDelete: %v", err)
	}
	if entry.Token != fakeToken {
		t.Errorf("token mismatch: got %q, want %q", entry.Token, fakeToken)
	}
	if entry.BotUsername != strings.ToLower(childUsername) {
		t.Errorf("username mismatch: got %q, want %q", entry.BotUsername, strings.ToLower(childUsername))
	}

	// Second poll must miss — token is one-time.
	if _, err := store.FetchAndDelete(ctx, n); !errors.Is(err, pairing.ErrNotFound) {
		t.Errorf("second fetch: want ErrNotFound, got %v", err)
	}
}

// TestEndToEnd_UserEditedUsername simulates a user editing the suggested
// username in Telegram's Create Bot dialog. nonce.Extract then fails, and
// the manager bot should fall back to DMing the creator. The pairing
// entry remains Waiting until it expires.
func TestEndToEnd_UserEditedUsername(t *testing.T) {
	ctx := context.Background()
	store := pairing.NewMemoryStore()

	n, _ := nonce.New()
	_ = store.Put(ctx, n, 5*time.Minute)

	// User edited the username to something arbitrary.
	edited := "mycoolbot"

	if _, ok := nonce.Extract(edited, "alva"); ok {
		t.Error("expected Extract to miss for user-edited username")
	}

	// Pairing entry stays Waiting; the client's next poll gets ErrNotReady.
	_, err := store.FetchAndDelete(ctx, n)
	if !errors.Is(err, pairing.ErrNotReady) {
		t.Errorf("pending fetch: want ErrNotReady, got %v", err)
	}
}

// TestEndToEnd_ExpiredPairing verifies that a pairing that times out
// (user abandoned the flow) returns ErrNotFound after TTL, not a stale
// token.
func TestEndToEnd_ExpiredPairing(t *testing.T) {
	ctx := context.Background()
	store := pairing.NewMemoryStore()

	n, _ := nonce.New()
	_ = store.Put(ctx, n, 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	// Manager bot arrives late — Complete should miss.
	err := store.Complete(ctx, n, "late:token", "late_bot")
	if !errors.Is(err, pairing.ErrNotFound) {
		t.Errorf("late Complete: want ErrNotFound, got %v", err)
	}

	// Client poll — also miss.
	_, err = store.FetchAndDelete(ctx, n)
	if !errors.Is(err, pairing.ErrNotFound) {
		t.Errorf("client poll after expiry: want ErrNotFound, got %v", err)
	}
}
