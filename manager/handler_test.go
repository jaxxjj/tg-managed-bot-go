package manager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alva-ai/tg-managed-bot-go/nonce"
	"github.com/alva-ai/tg-managed-bot-go/pairing"
	"github.com/alva-ai/tg-managed-bot-go/tgapi"
)

// fakeClient is a configurable test double for [Client].
type fakeClient struct {
	mu sync.Mutex

	// Callback hooks — nil means "not implemented for this test".
	getToken func(ctx context.Context, botID int64) (string, error)
	sendMsg  func(ctx context.Context, chatID int64, text string) error

	// Captured for later assertions.
	sentMsgs []sentMessage
}

type sentMessage struct {
	ChatID int64
	Text   string
}

func (f *fakeClient) GetManagedBotToken(ctx context.Context, botID int64) (string, error) {
	if f.getToken == nil {
		return "", errors.New("fakeClient.GetManagedBotToken not wired")
	}
	return f.getToken(ctx, botID)
}

func (f *fakeClient) SendMessage(ctx context.Context, chatID int64, text string) error {
	f.mu.Lock()
	f.sentMsgs = append(f.sentMsgs, sentMessage{ChatID: chatID, Text: text})
	f.mu.Unlock()
	if f.sendMsg == nil {
		return nil
	}
	return f.sendMsg(ctx, chatID, text)
}

func (f *fakeClient) dms() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sentMessage, len(f.sentMsgs))
	copy(out, f.sentMsgs)
	return out
}

// newUpdate builds a managed_bot_created update with the given username.
// creatorID = 0 omits the From field entirely (simulates channel posts).
func newUpdate(creatorID, botID int64, botUsername string) *tgapi.Update {
	u := &tgapi.Update{
		UpdateID: 1,
		Message: &tgapi.Message{
			MessageID: 1,
			Date:      time.Now().Unix(),
			ManagedBotCreated: &tgapi.ManagedBotCreated{
				Bot: tgapi.User{ID: botID, IsBot: true, FirstName: "Alva", Username: botUsername},
			},
		},
	}
	if creatorID != 0 {
		u.Message.From = &tgapi.User{ID: creatorID, FirstName: "Jaxon"}
	}
	return u
}

// ---------- No-op paths ----------

func TestHandleUpdate_NilSafe(t *testing.T) {
	h := &Handler{
		Store:  pairing.NewMemoryStore(),
		API:    &fakeClient{},
		Prefix: "alva",
	}
	cases := []*tgapi.Update{
		nil,
		{},
		{Message: nil},
		{Message: &tgapi.Message{}}, // no ManagedBotCreated
		{Message: &tgapi.Message{From: &tgapi.User{}}}, // still no event
	}
	for i, u := range cases {
		if err := h.HandleUpdate(context.Background(), u); err != nil {
			t.Errorf("case %d: unexpected error %v", i, err)
		}
	}
}

// ---------- Happy path ----------

func TestHandleUpdate_HappyPath(t *testing.T) {
	ctx := context.Background()
	store := pairing.NewMemoryStore()
	n, _ := nonce.New()
	username, err := nonce.PackIntoUsername("alva", n)
	if err != nil {
		t.Fatalf("PackIntoUsername: %v", err)
	}
	if err := store.Put(ctx, n, time.Minute); err != nil {
		t.Fatal(err)
	}

	const childToken = "8329495695:NEWTOKEN"
	fake := &fakeClient{
		getToken: func(_ context.Context, botID int64) (string, error) {
			if botID != 8329495695 {
				t.Errorf("bot id = %d", botID)
			}
			return childToken, nil
		},
	}
	h := &Handler{Store: store, API: fake, Prefix: "alva"}

	if err := h.HandleUpdate(ctx, newUpdate(42, 8329495695, username)); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}
	// Store should now have the pairing Ready with the fetched token.
	entry, err := store.FetchAndDelete(ctx, n)
	if err != nil {
		t.Fatalf("FetchAndDelete: %v", err)
	}
	if entry.Token != childToken {
		t.Errorf("token = %q, want %q", entry.Token, childToken)
	}
	if len(fake.dms()) != 0 {
		t.Errorf("unexpected DMs: %+v", fake.dms())
	}
}

// ---------- Nonce mismatch (user edited username) ----------

func TestHandleUpdate_NonceMismatch_RunsDefaultFallback(t *testing.T) {
	fake := &fakeClient{}
	h := &Handler{
		Store:  pairing.NewMemoryStore(),
		API:    fake,
		Prefix: "alva",
	}
	// User edited the suggested username to something unrelated.
	u := newUpdate(42, 100, "mycoolbot")

	if err := h.HandleUpdate(context.Background(), u); err != nil {
		t.Errorf("HandleUpdate: %v", err)
	}
	dms := fake.dms()
	if len(dms) != 1 {
		t.Fatalf("want 1 DM, got %d", len(dms))
	}
	if dms[0].ChatID != 42 {
		t.Errorf("DM chat = %d, want 42", dms[0].ChatID)
	}
	if !strings.Contains(dms[0].Text, "@mycoolbot") {
		t.Errorf("DM should mention the bot's username: %q", dms[0].Text)
	}
}

func TestHandleUpdate_NonceMismatch_CustomFallback(t *testing.T) {
	var captured struct {
		chatID int64
		bot    tgapi.User
	}
	fake := &fakeClient{}
	h := &Handler{
		Store:  pairing.NewMemoryStore(),
		API:    fake,
		Prefix: "alva",
		OnFallback: func(_ context.Context, chatID int64, bot tgapi.User) error {
			captured.chatID = chatID
			captured.bot = bot
			return nil
		},
	}
	u := newUpdate(42, 100, "mycoolbot")
	if err := h.HandleUpdate(context.Background(), u); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}
	if captured.chatID != 42 || captured.bot.Username != "mycoolbot" {
		t.Errorf("custom fallback captured %+v", captured)
	}
	// With a custom fallback set, default SendMessage path must NOT fire.
	if len(fake.dms()) != 0 {
		t.Errorf("custom fallback should have preempted SendMessage: %+v", fake.dms())
	}
}

func TestHandleUpdate_DefaultFallback_NoCreator(t *testing.T) {
	// When the creator's chat id is 0 (no From field), default fallback
	// should silently no-op instead of calling SendMessage(0, ...).
	fake := &fakeClient{}
	h := &Handler{
		Store:  pairing.NewMemoryStore(),
		API:    fake,
		Prefix: "alva",
	}
	u := newUpdate(0, 100, "mycoolbot")
	if err := h.HandleUpdate(context.Background(), u); err != nil {
		t.Errorf("HandleUpdate: %v", err)
	}
	if len(fake.dms()) != 0 {
		t.Errorf("should not DM when creator id unknown: %+v", fake.dms())
	}
}

// ---------- Bot deactivated before token fetch ----------

func TestHandleUpdate_BotDeactivated_RunsFallback(t *testing.T) {
	n, _ := nonce.New()
	username, _ := nonce.PackIntoUsername("alva", n)
	fake := &fakeClient{
		getToken: func(_ context.Context, _ int64) (string, error) {
			return "", fmt.Errorf("call: %w", tgapi.ErrBotDeactivated)
		},
	}

	var fallbackCalled bool
	h := &Handler{
		Store:  pairing.NewMemoryStore(),
		API:    fake,
		Prefix: "alva",
		OnFallback: func(_ context.Context, _ int64, _ tgapi.User) error {
			fallbackCalled = true
			return nil
		},
	}
	// Note we don't Put; fallback runs on GetToken failure, so the
	// pairing state doesn't matter.
	if err := h.HandleUpdate(context.Background(), newUpdate(42, 100, username)); err != nil {
		t.Errorf("HandleUpdate: %v", err)
	}
	if !fallbackCalled {
		t.Errorf("expected fallback to be called")
	}
}

// ---------- GetToken fails with other error (propagates) ----------

func TestHandleUpdate_GetTokenHardFailure_PropagatesError(t *testing.T) {
	n, _ := nonce.New()
	username, _ := nonce.PackIntoUsername("alva", n)
	apiErr := errors.New("network collapsed")
	fake := &fakeClient{
		getToken: func(_ context.Context, _ int64) (string, error) {
			return "", apiErr
		},
	}
	h := &Handler{
		Store:  pairing.NewMemoryStore(),
		API:    fake,
		Prefix: "alva",
	}
	err := h.HandleUpdate(context.Background(), newUpdate(42, 100, username))
	if !errors.Is(err, apiErr) {
		t.Errorf("want wrapped apiErr, got %v", err)
	}
	// Should NOT DM — this is retryable, caller retries.
	if len(fake.dms()) != 0 {
		t.Errorf("hard failure should not DM: %+v", fake.dms())
	}
}

// ---------- Pairing store errors ----------

func TestHandleUpdate_PairingExpired_RunsFallback(t *testing.T) {
	// Pairing never Put → Complete returns ErrNotFound → fallback.
	n, _ := nonce.New()
	username, _ := nonce.PackIntoUsername("alva", n)
	fake := &fakeClient{
		getToken: func(context.Context, int64) (string, error) { return "tok", nil },
	}

	var fallbackCalled bool
	h := &Handler{
		Store:  pairing.NewMemoryStore(),
		API:    fake,
		Prefix: "alva",
		OnFallback: func(context.Context, int64, tgapi.User) error {
			fallbackCalled = true
			return nil
		},
	}
	if err := h.HandleUpdate(context.Background(), newUpdate(42, 100, username)); err != nil {
		t.Errorf("HandleUpdate: %v", err)
	}
	if !fallbackCalled {
		t.Errorf("expected fallback for expired pairing")
	}
}

// TestHandleUpdate_AlreadyCompleted_Idempotent pins down the idempotency
// contract: when the pairing is already Ready (ErrInvalidState), a second
// delivery of the same managed_bot_created event is a silent success. It
// must NOT fire the fallback DM — doing so would confuse a user whose
// first delivery succeeded and who has already been handed their token.
//
// Triggers in practice:
//   - Telegram retrying the webhook because the first 2xx took too long.
//   - Two handler replicas observing the same update.
func TestHandleUpdate_AlreadyCompleted_Idempotent(t *testing.T) {
	ctx := context.Background()
	store := pairing.NewMemoryStore()
	n, _ := nonce.New()
	username, _ := nonce.PackIntoUsername("alva", n)
	_ = store.Put(ctx, n, time.Minute)
	_ = store.Complete(ctx, n, "prev", "prev_bot") // already Ready

	fake := &fakeClient{
		getToken: func(context.Context, int64) (string, error) { return "tok", nil },
	}
	var fallbackCalled bool
	h := &Handler{
		Store:  store,
		API:    fake,
		Prefix: "alva",
		OnFallback: func(context.Context, int64, tgapi.User) error {
			fallbackCalled = true
			return nil
		},
	}
	if err := h.HandleUpdate(ctx, newUpdate(42, 100, username)); err != nil {
		t.Errorf("HandleUpdate: %v", err)
	}
	if fallbackCalled {
		t.Errorf("duplicate delivery must NOT trigger fallback DM")
	}
	if len(fake.dms()) != 0 {
		t.Errorf("duplicate delivery must not emit any DMs: %+v", fake.dms())
	}
	// Pairing remains Ready with the original token — the retry did not
	// overwrite it.
	entry, err := store.FetchAndDelete(ctx, n)
	if err != nil {
		t.Fatalf("FetchAndDelete: %v", err)
	}
	if entry.Token != "prev" {
		t.Errorf("idempotent retry should preserve original token; got %q", entry.Token)
	}
}

// ---------- Fallback itself errors — must propagate ----------

func TestHandleUpdate_FallbackError_Propagates(t *testing.T) {
	fake := &fakeClient{}
	fallbackErr := errors.New("send DM failed")
	h := &Handler{
		Store:  pairing.NewMemoryStore(),
		API:    fake,
		Prefix: "alva",
		OnFallback: func(context.Context, int64, tgapi.User) error {
			return fallbackErr
		},
	}
	err := h.HandleUpdate(context.Background(), newUpdate(42, 100, "mycoolbot"))
	if !errors.Is(err, fallbackErr) {
		t.Errorf("want fallbackErr, got %v", err)
	}
}
