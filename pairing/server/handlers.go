package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/alva-ai/tg-managed-bot-go/link"
	"github.com/alva-ai/tg-managed-bot-go/nonce"
	"github.com/alva-ai/tg-managed-bot-go/pairing"
)

// MaxRequestBodyBytes caps the PUT /pair body size. A legitimate
// payload is well under 1 KiB (one token + one username); this limit
// protects against accidental or malicious large bodies.
const MaxRequestBodyBytes = 4 * 1024

// RegisterResponse is the body returned by [PostPair] (HTTP 201).
type RegisterResponse struct {
	Nonce     string `json:"nonce"`
	DeepLink  string `json:"deep_link"`
	ExpiresIn int    `json:"expires_in"` // seconds
}

// CompleteRequest is the body accepted by [PutPair].
type CompleteRequest struct {
	Token       string `json:"token"`
	BotUsername string `json:"bot_username"`
}

// TokenResponse is the body returned by [GetPair] on HTTP 200 (ready).
type TokenResponse struct {
	Token       string `json:"token"`
	BotUsername string `json:"bot_username"`
	CompletedAt string `json:"completed_at"` // RFC3339 UTC
}

// noStore wraps h so every response carries cache-busting headers.
// Pairing traffic is one-time bearer credentials; caching is unsafe.
func noStore(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		h(w, r)
	}
}

// writeJSON marshals v as JSON and writes it with the given status.
// Errors during encode/write are logged but not surfaced — at that
// point the response is already underway.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		zerolog.Ctx(r.Context()).Warn().
			Err(err).
			Str("path", r.URL.Path).
			Int("status", status).
			Msg("pairing: failed to encode response body")
	}
}

// writeError writes `{"error":"<msg>"}` with the given status.
func writeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	writeJSON(w, r, status, map[string]string{"error": msg})
}

// writeStatus writes `{"status":"<label>"}`. Used by [GetPair] to
// distinguish waiting vs not-found without bleeding protocol details.
func writeStatus(w http.ResponseWriter, r *http.Request, status int, label string) {
	writeJSON(w, r, status, map[string]string{"status": label})
}

// PostPair returns the handler for "POST /pair": generate a nonce,
// register it in cfg.Store with PairingTTL, and return the deep link
// to present to the user.
func PostPair(cfg Config) http.HandlerFunc {
	ttl := cfg.effectiveTTL()
	return noStore(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := zerolog.Ctx(ctx)

		n, err := nonce.New()
		if err != nil {
			logger.Error().Err(err).Msg("pairing: nonce.New failed")
			writeError(w, r, http.StatusInternalServerError, "internal error")
			return
		}
		if err := cfg.Store.Put(ctx, n, ttl); err != nil {
			logger.Error().Err(err).Msg("pairing: store.Put failed")
			writeError(w, r, http.StatusInternalServerError, "internal error")
			return
		}
		botUsername, err := nonce.PackIntoUsername(cfg.NoncePrefix, n)
		if err != nil {
			// Misconfigured prefix: surface to the operator, not the caller.
			logger.Error().Err(err).
				Str("prefix", cfg.NoncePrefix).
				Msg("pairing: PackIntoUsername failed — check Config.NoncePrefix")
			writeError(w, r, http.StatusInternalServerError, "internal error")
			return
		}
		deepLink, err := link.BuildNewBot(link.Options{
			ManagerBotUsername: cfg.ManagerBotUsername,
			SuggestedUsername:  botUsername,
			SuggestedName:      cfg.SuggestedName,
		})
		if err != nil {
			logger.Error().Err(err).Msg("pairing: link.BuildNewBot failed")
			writeError(w, r, http.StatusInternalServerError, "internal error")
			return
		}

		logger.Debug().
			Str("nonce", n).
			Dur("ttl", ttl).
			Msg("pairing: registered")

		writeJSON(w, r, http.StatusCreated, RegisterResponse{
			Nonce:     n,
			DeepLink:  deepLink,
			ExpiresIn: int(ttl.Seconds()),
		})
	})
}

// PutPair returns the handler for "PUT /pair/{nonce}": authenticate
// the caller (manager bot), decode the token+username body, and mark
// the pairing Ready.
func PutPair(cfg Config) http.HandlerFunc {
	extractNonce := cfg.effectiveNonceExtractor()
	return noStore(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := zerolog.Ctx(ctx)

		if !cfg.Authenticator(r) {
			logger.Warn().
				Str("remote", r.RemoteAddr).
				Msg("pairing: PUT rejected (auth failed)")
			writeError(w, r, http.StatusUnauthorized, "invalid bearer")
			return
		}

		n := extractNonce(r)
		if err := nonce.Validate(n); err != nil {
			writeError(w, r, http.StatusBadRequest, err.Error())
			return
		}

		var body CompleteRequest
		dec := json.NewDecoder(io.LimitReader(r.Body, MaxRequestBodyBytes))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			writeError(w, r, http.StatusBadRequest, "malformed body: "+err.Error())
			return
		}
		if body.Token == "" || body.BotUsername == "" {
			writeError(w, r, http.StatusBadRequest, "token and bot_username are required")
			return
		}

		switch err := cfg.Store.Complete(ctx, n, body.Token, body.BotUsername); {
		case err == nil:
			logger.Info().
				Str("nonce", n).
				Str("bot_username", body.BotUsername).
				Msg("pairing: completed")
			writeJSON(w, r, http.StatusOK, map[string]bool{"ok": true})
		case errors.Is(err, pairing.ErrNotFound):
			writeError(w, r, http.StatusNotFound, "nonce not found or expired")
		case errors.Is(err, pairing.ErrInvalidState):
			writeError(w, r, http.StatusConflict, "nonce already completed")
		default:
			logger.Error().Err(err).Str("nonce", n).Msg("pairing: store.Complete failed")
			writeError(w, r, http.StatusInternalServerError, "internal error")
		}
	})
}

// GetPair returns the handler for "GET /pair/{nonce}": poll the store.
// 200 returns the token (one-time). 404 distinguishes "still waiting"
// from "never existed or already consumed" via the body's status field.
func GetPair(cfg Config) http.HandlerFunc {
	extractNonce := cfg.effectiveNonceExtractor()
	return noStore(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := zerolog.Ctx(ctx)

		n := extractNonce(r)
		if err := nonce.Validate(n); err != nil {
			writeError(w, r, http.StatusBadRequest, err.Error())
			return
		}

		entry, err := cfg.Store.FetchAndDelete(ctx, n)
		switch {
		case err == nil:
			logger.Debug().Str("nonce", n).Msg("pairing: fetched (and deleted)")
			writeJSON(w, r, http.StatusOK, TokenResponse{
				Token:       entry.Token,
				BotUsername: entry.BotUsername,
				CompletedAt: entry.CompletedAt.UTC().Format(time.RFC3339),
			})
		case errors.Is(err, pairing.ErrNotReady):
			writeStatus(w, r, http.StatusNotFound, "waiting")
		case errors.Is(err, pairing.ErrNotFound):
			writeStatus(w, r, http.StatusNotFound, "not_found")
		default:
			logger.Error().Err(err).Str("nonce", n).Msg("pairing: store.FetchAndDelete failed")
			writeError(w, r, http.StatusInternalServerError, "internal error")
		}
	})
}
