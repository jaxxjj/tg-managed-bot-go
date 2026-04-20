package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alva-ai/tg-managed-bot-go/nonce"
	"github.com/alva-ai/tg-managed-bot-go/pairing"
)

// fixture builds a fully-configured httptest.Server around Mount.
// Returns the server plus the Config so tests can cross-check.
type fixture struct {
	srv *httptest.Server
	cfg Config
}

func newFixture(t *testing.T, overrides ...func(*Config)) *fixture {
	t.Helper()

	cfg := Config{
		Store:              pairing.NewMemoryStore(),
		ManagerBotUsername: "alva_manager_bot",
		NoncePrefix:        "alva",
		SuggestedName:      "My Alva",
		PairingTTL:         30 * time.Second,
		Authenticator:      BearerAuth("test-secret"),
	}
	for _, o := range overrides {
		o(&cfg)
	}

	mux := http.NewServeMux()
	if err := Mount(mux, cfg, "/api/v1"); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &fixture{srv: srv, cfg: cfg}
}

// do is a small HTTP helper returning (status, body, cache-control-header).
func (f *fixture) do(t *testing.T, method, path, auth string, body any) (int, []byte, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, f.srv.URL+path, reader)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header.Get("Cache-Control")
}

// ---------- Config.Validate ----------

func TestConfig_Validate(t *testing.T) {
	base := Config{
		Store:              pairing.NewMemoryStore(),
		ManagerBotUsername: "alva_manager_bot",
		NoncePrefix:        "alva",
		Authenticator:      BearerAuth("x"),
	}
	if err := base.Validate(); err != nil {
		t.Errorf("base should validate: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"no Store", func(c *Config) { c.Store = nil }},
		{"no manager", func(c *Config) { c.ManagerBotUsername = "" }},
		{"no prefix", func(c *Config) { c.NoncePrefix = "" }},
		{"no authn", func(c *Config) { c.Authenticator = nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := base
			c.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Errorf("expected error")
			}
		})
	}
}

func TestMount_RejectsBadPrefix(t *testing.T) {
	cfg := Config{
		Store:              pairing.NewMemoryStore(),
		ManagerBotUsername: "alva_manager_bot",
		NoncePrefix:        "alva",
		Authenticator:      BearerAuth("x"),
	}
	err := Mount(http.NewServeMux(), cfg, "no-leading-slash")
	if err == nil {
		t.Errorf("expected error for prefix without leading slash")
	}
}

func TestMount_AllowsEmptyPrefix(t *testing.T) {
	cfg := Config{
		Store:              pairing.NewMemoryStore(),
		ManagerBotUsername: "alva_manager_bot",
		NoncePrefix:        "alva",
		Authenticator:      BearerAuth("x"),
	}
	if err := Mount(http.NewServeMux(), cfg, ""); err != nil {
		t.Errorf("empty prefix should be allowed: %v", err)
	}
}

func TestBearerAuth(t *testing.T) {
	authn := BearerAuth("my-secret")

	cases := []struct {
		name string
		hdr  string
		want bool
	}{
		{"correct", "Bearer my-secret", true},
		{"wrong secret", "Bearer other", false},
		{"no bearer prefix", "my-secret", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequestWithContext(context.Background(), "PUT", "/", nil)
			if c.hdr != "" {
				req.Header.Set("Authorization", c.hdr)
			}
			if got := authn(req); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestBearerAuth_EmptySecretPanics(t *testing.T) {
	// Regression for Codex P1: BearerAuth("") must fail closed at
	// construction. An empty secret would otherwise accept a bare
	// "Authorization: Bearer " header and silently enable anyone to
	// call PUT /pair/{nonce}.
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("BearerAuth(\"\") should panic; it did not")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value is %T, want string", r)
		}
		if !strings.Contains(msg, "empty secret") {
			t.Errorf("panic message should mention empty secret; got %q", msg)
		}
	}()
	_ = BearerAuth("")
}

// ---------- End-to-end HTTP flow ----------

func TestFlow_HappyPath(t *testing.T) {
	f := newFixture(t)

	// 1. POST /pair
	status, body, cc := f.do(t, "POST", "/api/v1/pair", "", nil)
	if status != http.StatusCreated {
		t.Fatalf("POST /pair: status = %d, body = %s", status, body)
	}
	if !strings.Contains(cc, "no-store") {
		t.Errorf("POST missing no-store: %q", cc)
	}
	var reg RegisterResponse
	if err := json.Unmarshal(body, &reg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if reg.Nonce == "" || !strings.HasPrefix(reg.DeepLink, "https://t.me/newbot/alva_manager_bot/alva_") {
		t.Errorf("unexpected register response: %+v", reg)
	}
	if reg.ExpiresIn != 30 {
		t.Errorf("ExpiresIn = %d, want 30", reg.ExpiresIn)
	}

	// 2. GET before complete → 404 waiting
	status, body, _ = f.do(t, "GET", "/api/v1/pair/"+reg.Nonce, "", nil)
	if status != http.StatusNotFound {
		t.Errorf("GET waiting: status = %d, body = %s", status, body)
	}
	if !strings.Contains(string(body), `"status":"waiting"`) {
		t.Errorf("GET waiting body: %s", body)
	}

	// 3. PUT with correct bearer
	expectedUsername := "alva_" + reg.Nonce + "_bot"
	status, body, _ = f.do(t, "PUT", "/api/v1/pair/"+reg.Nonce, "Bearer test-secret", CompleteRequest{
		Token:       "123:ABCTOKEN",
		BotUsername: expectedUsername,
	})
	if status != http.StatusOK {
		t.Errorf("PUT: status = %d, body = %s", status, body)
	}

	// 4. GET → 200 with token
	status, body, cc = f.do(t, "GET", "/api/v1/pair/"+reg.Nonce, "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET ready: status = %d, body = %s", status, body)
	}
	if !strings.Contains(cc, "no-store") {
		t.Errorf("GET ready missing no-store: %q", cc)
	}
	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if tok.Token != "123:ABCTOKEN" || tok.BotUsername != expectedUsername {
		t.Errorf("token response: %+v", tok)
	}

	// 5. GET again → 404 not_found (one-time use)
	status, body, _ = f.do(t, "GET", "/api/v1/pair/"+reg.Nonce, "", nil)
	if status != http.StatusNotFound {
		t.Errorf("GET after consume: status = %d", status)
	}
	if !strings.Contains(string(body), `"status":"not_found"`) {
		t.Errorf("GET after consume body: %s", body)
	}
}

func TestPutPair_RejectsWithoutAuth(t *testing.T) {
	f := newFixture(t)
	// Need a valid nonce to reach the auth check cleanly.
	n, _ := nonce.New()
	_ = f.cfg.Store.Put(context.Background(), n, time.Minute)

	status, _, _ := f.do(t, "PUT", "/api/v1/pair/"+n, "", CompleteRequest{Token: "x", BotUsername: "x_bot"})
	if status != http.StatusUnauthorized {
		t.Errorf("no auth: status = %d, want 401", status)
	}

	status, _, _ = f.do(t, "PUT", "/api/v1/pair/"+n, "Bearer wrong", CompleteRequest{Token: "x", BotUsername: "x_bot"})
	if status != http.StatusUnauthorized {
		t.Errorf("wrong auth: status = %d, want 401", status)
	}
}

func TestPutPair_MissingFields(t *testing.T) {
	f := newFixture(t)
	n, _ := nonce.New()
	_ = f.cfg.Store.Put(context.Background(), n, time.Minute)

	cases := []CompleteRequest{
		{},
		{Token: "x"},
		{BotUsername: "x_bot"},
	}
	for i, body := range cases {
		status, respBody, _ := f.do(t, "PUT", "/api/v1/pair/"+n, "Bearer test-secret", body)
		if status != http.StatusBadRequest {
			t.Errorf("case %d: status = %d, body = %s", i, status, respBody)
		}
	}
}

func TestPutPair_UnknownNonce(t *testing.T) {
	f := newFixture(t)
	// 12-char valid nonce that was never Put.
	status, _, _ := f.do(t, "PUT", "/api/v1/pair/aaaaaaaaaaaa", "Bearer test-secret",
		CompleteRequest{Token: "x", BotUsername: "x_bot"})
	if status != http.StatusNotFound {
		t.Errorf("unknown nonce: status = %d, want 404", status)
	}
}

func TestPutPair_DoubleComplete(t *testing.T) {
	f := newFixture(t)
	n, _ := nonce.New()
	_ = f.cfg.Store.Put(context.Background(), n, time.Minute)

	// First complete succeeds.
	if s, _, _ := f.do(t, "PUT", "/api/v1/pair/"+n, "Bearer test-secret",
		CompleteRequest{Token: "x", BotUsername: "x_bot"}); s != http.StatusOK {
		t.Fatalf("first PUT: %d", s)
	}

	// Second complete on same nonce → 409 Conflict.
	s, _, _ := f.do(t, "PUT", "/api/v1/pair/"+n, "Bearer test-secret",
		CompleteRequest{Token: "y", BotUsername: "y_bot"})
	if s != http.StatusConflict {
		t.Errorf("second PUT: status = %d, want 409", s)
	}
}

func TestGetPair_BadNonce(t *testing.T) {
	f := newFixture(t)
	status, _, _ := f.do(t, "GET", "/api/v1/pair/shortx", "", nil)
	if status != http.StatusBadRequest {
		t.Errorf("invalid nonce: status = %d, want 400", status)
	}
}

func TestPutPair_RejectsUnknownFields(t *testing.T) {
	// DisallowUnknownFields should reject typos and injected fields.
	f := newFixture(t)
	n, _ := nonce.New()
	_ = f.cfg.Store.Put(context.Background(), n, time.Minute)

	payload := `{"token":"x","bot_username":"x_bot","evil":"injected"}`
	req, _ := http.NewRequestWithContext(context.Background(),
		"PUT", f.srv.URL+"/api/v1/pair/"+n, strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown field: status = %d, want 400", resp.StatusCode)
	}
}

// ---------- Defaults ----------

func TestPostPair_DefaultTTL(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.PairingTTL = 0 }) // use default

	status, body, _ := f.do(t, "POST", "/api/v1/pair", "", nil)
	if status != http.StatusCreated {
		t.Fatalf("status = %d", status)
	}
	var reg RegisterResponse
	_ = json.Unmarshal(body, &reg)
	want := int(DefaultPairingTTL.Seconds())
	if reg.ExpiresIn != want {
		t.Errorf("ExpiresIn = %d, want default %d", reg.ExpiresIn, want)
	}
}

// ---------- noStore middleware on every path ----------

func TestAllRoutes_NoStoreHeader(t *testing.T) {
	f := newFixture(t)
	n, _ := nonce.New()
	_ = f.cfg.Store.Put(context.Background(), n, time.Minute)

	cases := []struct {
		method, path string
		auth         string
		body         any
	}{
		{"POST", "/api/v1/pair", "", nil},
		{"PUT", "/api/v1/pair/" + n, "Bearer test-secret", CompleteRequest{Token: "x", BotUsername: "x_bot"}},
		{"GET", "/api/v1/pair/" + n, "", nil},
		{"PUT", "/api/v1/pair/" + n, "", nil}, // 401 path
	}
	for _, c := range cases {
		_, _, cc := f.do(t, c.method, c.path, c.auth, c.body)
		if !strings.Contains(cc, "no-store") {
			t.Errorf("%s %s: missing no-store, got %q", c.method, c.path, cc)
		}
	}
}
