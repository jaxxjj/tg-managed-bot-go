// Package tgmanagedbot_test exercises the packages together end-to-end to
// catch drift across module boundaries. The suite runs entirely in-
// process — a httptest server impersonates the Telegram Bot API, an
// httptest.NewServer hosts the real pairing/server handlers, and
// manager.Handler / pairing.Store are wired against both. No Docker,
// no ngrok, no real Telegram.
//
// Rationale: the functional bug Gemini surfaced on PR #2
// (gin.WrapF + r.PathValue mismatch) would have been caught by a test
// like this that actually wires the packages together. Single-package
// unit tests had 100% coverage and still let it through because every
// test was mocked at the same boundary.
package tgmanagedbot_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alva-ai/tg-managed-bot-go/link"
	"github.com/alva-ai/tg-managed-bot-go/manager"
	"github.com/alva-ai/tg-managed-bot-go/nonce"
	"github.com/alva-ai/tg-managed-bot-go/pairing"
	"github.com/alva-ai/tg-managed-bot-go/pairing/server"
	"github.com/alva-ai/tg-managed-bot-go/reconcile"
	"github.com/alva-ai/tg-managed-bot-go/tgapi"
)

// fakeTelegram is an httptest.Server impersonating the Telegram Bot API.
// Only the methods used by the cross-package test are implemented;
// anything else returns an error so tests fail loudly rather than
// silently accepting unexpected traffic.
type fakeTelegram struct {
	srv *httptest.Server

	mu                     sync.Mutex
	managedBotTokens       map[int64]string // bot_id → current token
	sendMessageCalls       []fakeSendMessage
	managedBotUnauthorized map[int64]bool // bot_id → token is revoked
	managedBotDeactivated  map[int64]bool // bot_id → user deleted bot
}

type fakeSendMessage struct {
	ChatID int64
	Text   string
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	ft := &fakeTelegram{
		managedBotTokens:       make(map[int64]string),
		managedBotUnauthorized: make(map[int64]bool),
		managedBotDeactivated:  make(map[int64]bool),
	}
	ft.srv = httptest.NewServer(http.HandlerFunc(ft.handle))
	t.Cleanup(ft.srv.Close)
	return ft
}

// setManagedBot pre-seeds a bot_id → token mapping so subsequent
// getManagedBotToken calls for that id return this token.
func (ft *fakeTelegram) setManagedBot(botID int64, token string) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.managedBotTokens[botID] = token
}

// deactivateBot simulates the user deleting the bot via @BotFather.
// Subsequent getManagedBotToken calls return 403 "user is deactivated".
func (ft *fakeTelegram) deactivateBot(botID int64) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.managedBotDeactivated[botID] = true
}

func (ft *fakeTelegram) sentMessages() []fakeSendMessage {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	out := make([]fakeSendMessage, len(ft.sendMessageCalls))
	copy(out, ft.sendMessageCalls)
	return out
}

// handle dispatches /bot<TOKEN>/<method> requests.
func (ft *fakeTelegram) handle(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "bot") {
		writeAPIErr(w, 404, "Not Found: unknown route "+r.URL.Path)
		return
	}
	method := parts[1]
	switch method {
	case "getMe":
		ft.handleGetMe(w)
	case "getManagedBotToken":
		ft.handleGetManagedBotToken(w, r)
	case "sendMessage":
		ft.handleSendMessage(w, r)
	default:
		writeAPIErr(w, 404, "Not Found: method "+method)
	}
}

func (ft *fakeTelegram) handleGetMe(w http.ResponseWriter) {
	writeAPIOK(w, `{
		"id": 8322915824,
		"is_bot": true,
		"first_name": "Alva Manager",
		"username": "alva_manager_bot",
		"can_manage_bots": true
	}`)
}

func (ft *fakeTelegram) handleGetManagedBotToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIErr(w, 400, "Bad Request")
		return
	}
	ft.mu.Lock()
	defer ft.mu.Unlock()

	if ft.managedBotDeactivated[req.UserID] {
		writeAPIErr(w, 403, "Forbidden: user is deactivated")
		return
	}
	if ft.managedBotUnauthorized[req.UserID] {
		writeAPIErr(w, 401, "Unauthorized")
		return
	}
	token, ok := ft.managedBotTokens[req.UserID]
	if !ok {
		writeAPIErr(w, 400, "Bad Request: bot_id not managed")
		return
	}
	b, _ := json.Marshal(token)
	writeAPIOK(w, string(b))
}

func (ft *fakeTelegram) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIErr(w, 400, "Bad Request")
		return
	}
	ft.mu.Lock()
	ft.sendMessageCalls = append(ft.sendMessageCalls, fakeSendMessage{ChatID: req.ChatID, Text: req.Text})
	ft.mu.Unlock()
	writeAPIOK(w, `{"message_id": 1, "date": 0}`)
}

func writeAPIOK(w http.ResponseWriter, resultJSON string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"ok":true,"result":`+resultJSON+`}`)
}

func writeAPIErr(w http.ResponseWriter, code int, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	b, _ := json.Marshal(map[string]any{
		"ok": false, "error_code": code, "description": description,
	})
	_, _ = w.Write(b)
}

// ---------- Wired fixture ----------

// crossFixture wires every package together against a fake Telegram.
// Resembles the shape of the alva-like example, minus gin.
type crossFixture struct {
	t          *testing.T
	store      pairing.Store
	tg         *fakeTelegram
	managerAPI *tgapi.Client
	handler    *manager.Handler
	server     *httptest.Server
	secret     string
}

func newCrossFixture(t *testing.T) *crossFixture {
	t.Helper()

	tg := newFakeTelegram(t)

	store := pairing.NewMemoryStore()
	managerAPI := tgapi.NewClient("manager-bot-token", tgapi.WithBaseURL(tg.srv.URL))
	const secret = "integration-test-secret"

	handler := &manager.Handler{
		Store:  store,
		API:    managerAPI,
		Prefix: "alva",
	}

	pairCfg := server.Config{
		Store:              store,
		ManagerBotUsername: "alva_manager_bot",
		NoncePrefix:        "alva",
		SuggestedName:      "My Alva",
		PairingTTL:         time.Minute,
		Authenticator:      server.BearerAuth(secret),
	}

	mux := http.NewServeMux()
	if err := server.Mount(mux, pairCfg, "/api/v1"); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &crossFixture{
		t:          t,
		store:      store,
		tg:         tg,
		managerAPI: managerAPI,
		handler:    handler,
		server:     srv,
		secret:     secret,
	}
}

// httpDo runs an HTTP call against the pairing server and returns
// (status, body).
func (f *crossFixture) httpDo(method, path, auth string, body any) (int, []byte) {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, f.server.URL+path, reader)
	if err != nil {
		f.t.Fatalf("new req: %v", err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// simulateManagedBotCreated dispatches a synthetic managed_bot_created
// event to manager.Handler, mimicking what the manager bot's webhook
// would deliver after a user taps Create in the Telegram dialog.
func (f *crossFixture) simulateManagedBotCreated(creatorTgID, botID int64, botUsername string) error {
	return f.handler.HandleUpdate(context.Background(), &tgapi.Update{
		UpdateID: 1,
		Message: &tgapi.Message{
			MessageID: 1,
			From:      &tgapi.User{ID: creatorTgID, FirstName: "TestUser"},
			Chat:      &tgapi.Chat{ID: creatorTgID, Type: "private"},
			Date:      time.Now().Unix(),
			ManagedBotCreated: &tgapi.ManagedBotCreated{
				Bot: tgapi.User{
					ID:        botID,
					IsBot:     true,
					FirstName: "Alva Test",
					Username:  botUsername,
				},
			},
		},
	})
}

// ---------- Tests ----------

// TestCrossPackage_HappyPath wires every package together and walks the
// full pairing flow. Catches the cross-package bug class (e.g. router
// path-value mismatch) that single-package unit tests miss.
func TestCrossPackage_HappyPath(t *testing.T) {
	f := newCrossFixture(t)

	// 1. Client: POST /api/v1/pair  → nonce + deep link.
	status, body := f.httpDo("POST", "/api/v1/pair", "", nil)
	if status != http.StatusCreated {
		t.Fatalf("POST /pair: status=%d body=%s", status, body)
	}
	var reg struct {
		Nonce    string `json:"nonce"`
		DeepLink string `json:"deep_link"`
	}
	if err := json.Unmarshal(body, &reg); err != nil {
		t.Fatal(err)
	}
	if reg.Nonce == "" {
		t.Fatalf("no nonce in register response: %s", body)
	}
	// Sanity-check the deep link by parsing the username back.
	_ = link.BuildNewBot // ensure link package still used
	expectedUsername, err := nonce.PackIntoUsername("alva", reg.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reg.DeepLink, expectedUsername) {
		t.Errorf("deep link %q does not contain expected username %q", reg.DeepLink, expectedUsername)
	}

	// 2. Pretend Telegram mints a bot with the suggested username.
	const childBotID int64 = 111222333
	const childToken = "111222333:FAKECHILDTOKEN"
	f.tg.setManagedBot(childBotID, childToken)

	// 3. Deliver a managed_bot_created event to manager.Handler —
	//    this is what the manager bot's webhook would feed us.
	if err := f.simulateManagedBotCreated(42, childBotID, expectedUsername); err != nil {
		t.Fatalf("handler: %v", err)
	}

	// 4. Client: GET /api/v1/pair/:nonce  → 200 with the fake token.
	status, body = f.httpDo("GET", "/api/v1/pair/"+reg.Nonce, "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /pair: status=%d body=%s", status, body)
	}
	var tok struct {
		Token       string `json:"token"`
		BotUsername string `json:"bot_username"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		t.Fatal(err)
	}
	if tok.Token != childToken {
		t.Errorf("got token %q, want %q (fake TG return)", tok.Token, childToken)
	}
	if tok.BotUsername != expectedUsername {
		t.Errorf("got bot_username %q, want %q", tok.BotUsername, expectedUsername)
	}

	// 5. Second GET → 404 not_found (one-time).
	status, body = f.httpDo("GET", "/api/v1/pair/"+reg.Nonce, "", nil)
	if status != http.StatusNotFound || !strings.Contains(string(body), "not_found") {
		t.Errorf("second GET: status=%d body=%s", status, body)
	}

	// 6. No DMs fired (happy path uses no fallback).
	if dms := f.tg.sentMessages(); len(dms) != 0 {
		t.Errorf("unexpected fallback DMs: %+v", dms)
	}
}

// TestCrossPackage_NonceMismatchFallback verifies that when the user
// edits the suggested username (so nonce.Extract misses), the full
// stack gracefully falls back to DMing the creator via the manager bot.
// Exercises: HTTP register → handler (nonce-miss path) → tgapi.SendMessage
// → pending pairing remains Waiting.
func TestCrossPackage_NonceMismatchFallback(t *testing.T) {
	f := newCrossFixture(t)

	// Register a pairing.
	status, body := f.httpDo("POST", "/api/v1/pair", "", nil)
	if status != http.StatusCreated {
		t.Fatalf("POST /pair: %d %s", status, body)
	}
	var reg struct {
		Nonce string `json:"nonce"`
	}
	_ = json.Unmarshal(body, &reg)

	// User edited the suggested username.
	const editedUsername = "userscoolbot"
	const childBotID int64 = 999888777
	f.tg.setManagedBot(childBotID, "wont-be-used-because-mismatch")

	// Deliver the event. Handler should detect mismatch → send DM →
	// leave pairing Waiting (no Complete).
	if err := f.simulateManagedBotCreated(42, childBotID, editedUsername); err != nil {
		t.Fatalf("handler: %v", err)
	}

	// Verify: DM fired to creator (id=42).
	dms := f.tg.sentMessages()
	if len(dms) != 1 {
		t.Fatalf("want 1 DM, got %d: %+v", len(dms), dms)
	}
	if dms[0].ChatID != 42 {
		t.Errorf("DM chat = %d, want 42", dms[0].ChatID)
	}
	if !strings.Contains(dms[0].Text, "@"+editedUsername) {
		t.Errorf("DM should reference the edited username: %q", dms[0].Text)
	}

	// Verify: the pairing is still Waiting (client polls → 404 waiting).
	status, body = f.httpDo("GET", "/api/v1/pair/"+reg.Nonce, "", nil)
	if status != http.StatusNotFound || !strings.Contains(string(body), "waiting") {
		t.Errorf("expected 404 waiting, got %d %s", status, body)
	}
}

// TestCrossPackage_BotDeactivatedDuringCreation covers the race where the
// user taps Create then immediately deletes the bot from @BotFather.
// Handler gets ErrBotDeactivated from GetManagedBotToken → falls back
// to DM instead of Completing the pairing.
func TestCrossPackage_BotDeactivatedDuringCreation(t *testing.T) {
	f := newCrossFixture(t)

	status, body := f.httpDo("POST", "/api/v1/pair", "", nil)
	if status != http.StatusCreated {
		t.Fatalf("POST /pair: %d %s", status, body)
	}
	var reg struct {
		Nonce string `json:"nonce"`
	}
	_ = json.Unmarshal(body, &reg)

	const childBotID int64 = 555
	username, _ := nonce.PackIntoUsername("alva", reg.Nonce)
	// User confirmed and then deleted the bot before we fetched the token.
	f.tg.deactivateBot(childBotID)

	if err := f.simulateManagedBotCreated(42, childBotID, username); err != nil {
		t.Fatalf("handler: %v", err)
	}

	// Fallback DM fired.
	if len(f.tg.sentMessages()) != 1 {
		t.Errorf("want 1 fallback DM, got %d", len(f.tg.sentMessages()))
	}
	// Pairing still Waiting.
	status, _ = f.httpDo("GET", "/api/v1/pair/"+reg.Nonce, "", nil)
	if status != http.StatusNotFound {
		t.Errorf("expected 404, got %d", status)
	}
}

// TestCrossPackage_PutEndpointWithBearer exercises the manual completion
// path — PUT /pair/:nonce — which is how hermes-style external manager
// workers deliver the token without going through manager.Handler.
// This is the only path that requires the BearerAuth guard to actually fire.
func TestCrossPackage_PutEndpointWithBearer(t *testing.T) {
	f := newCrossFixture(t)

	status, body := f.httpDo("POST", "/api/v1/pair", "", nil)
	if status != http.StatusCreated {
		t.Fatalf("POST /pair: %d %s", status, body)
	}
	var reg struct {
		Nonce string `json:"nonce"`
	}
	_ = json.Unmarshal(body, &reg)
	expectedUsername, _ := nonce.PackIntoUsername("alva", reg.Nonce)

	// No bearer → 401.
	status, _ = f.httpDo("PUT", "/api/v1/pair/"+reg.Nonce, "", map[string]string{
		"token": "t", "bot_username": expectedUsername,
	})
	if status != http.StatusUnauthorized {
		t.Errorf("no bearer: got %d, want 401", status)
	}

	// Correct bearer → 200.
	const externalToken = "777:EXTERNALTOKEN"
	status, _ = f.httpDo("PUT", "/api/v1/pair/"+reg.Nonce, "Bearer "+f.secret,
		map[string]string{"token": externalToken, "bot_username": expectedUsername})
	if status != http.StatusOK {
		t.Errorf("PUT with bearer: got %d, want 200", status)
	}

	// GET returns the externally-delivered token.
	status, body = f.httpDo("GET", "/api/v1/pair/"+reg.Nonce, "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET: %d %s", status, body)
	}
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &tok)
	if tok.Token != externalToken {
		t.Errorf("got token %q, want %q", tok.Token, externalToken)
	}
}

// TestCrossPackage_ReconcileWiring verifies reconcile.CheckDrift plugs
// cleanly into a live tgapi.Client talking to the fake TG server.
// Catches shape mismatches between tgapi types and reconcile State.
func TestCrossPackage_ReconcileWiring(t *testing.T) {
	f := newCrossFixture(t)
	ctx := context.Background()

	me, err := f.managerAPI.GetMe(ctx)
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}

	// State matches what GetMe returned → zero drift expected.
	drifts := reconcile.CheckDrift(
		reconcile.State{
			BotID:             me.ID,
			ExpectPrivacyOff:  me.CanReadAllGroupMessages,
			LastKnownUsername: me.Username,
		},
		reconcile.Observed{Me: me},
	)
	if len(drifts) != 0 {
		t.Errorf("in-sync state should have zero drift, got %v", drifts)
	}

	// Now force a transport error on getManagedBotToken (simulating a
	// dead child bot) — reconcile should surface DriftDeleted.
	_, err = f.managerAPI.GetManagedBotToken(ctx, 999) // not seeded → 400 bad_request
	if err == nil {
		t.Fatal("expected error for unseeded bot")
	}

	f.tg.deactivateBot(42)
	_, err = f.managerAPI.GetManagedBotToken(ctx, 42)
	if !errors.Is(err, tgapi.ErrBotDeactivated) {
		t.Fatalf("want ErrBotDeactivated, got %v", err)
	}
	// The wiring: reconcile sees the same error from GetMe on a child
	// client. We simulate that here by checking the drift result.
	drifts = reconcile.CheckDrift(
		reconcile.State{BotID: 42},
		reconcile.Observed{GetMeErr: err},
	)
	if len(drifts) != 1 || drifts[0].Kind != reconcile.DriftDeleted {
		t.Errorf("want [DriftDeleted], got %v", drifts)
	}
}
