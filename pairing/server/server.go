package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alva-ai/tg-managed-bot-go/pairing"
)

// DefaultPairingTTL is applied to [Config.PairingTTL] when left at zero.
// 15 minutes matches the original Alva /start-code flow; long enough
// to finish a mobile-to-desktop handoff, short enough that abandoned
// pairings expire without operator intervention.
const DefaultPairingTTL = 15 * time.Minute

// Config is the runtime configuration for the pairing HTTP handlers.
// Build one and pass it to [Mount] or the individual handler factories.
//
// A zero-value Config is not usable; call [Config.Validate] (called
// automatically by [Mount]) for the specific reason.
type Config struct {
	// Store persists the pairing lifecycle. Any [pairing.Store]
	// implementation works; for production use the Redis-backed
	// store in the pairing/stores/redis subpackage.
	Store pairing.Store

	// ManagerBotUsername is the @-handle (without '@') of the bot that
	// users tap the deep link against. The bot must have Bot Management
	// Mode enabled in @BotFather.
	ManagerBotUsername string

	// NoncePrefix is the prefix embedded in the suggested child-bot
	// username (<prefix>_<nonce>_bot). The manager-side handler uses
	// the same prefix when extracting nonces from managed_bot_created
	// events, so this must match [github.com/alva-ai/tg-managed-bot-go/manager.Handler.Prefix]
	// when the two sides are connected.
	NoncePrefix string

	// SuggestedName is the display name pre-filled in Telegram's
	// "Create Bot" dialog. May be empty (users then pick their own).
	SuggestedName string

	// PairingTTL is how long a registered nonce stays valid before
	// Store.FetchAndDelete starts returning ErrNotFound. Default
	// [DefaultPairingTTL].
	PairingTTL time.Duration

	// Authenticator gates the PUT /pair/{nonce} endpoint. It receives
	// the incoming request and returns true iff the caller is
	// authorized (typically a manager bot worker). May not be nil —
	// an unauthenticated PUT would let anyone inject arbitrary tokens
	// for arbitrary nonces. Use [BearerAuth] for a shared-secret
	// implementation.
	Authenticator func(*http.Request) bool
}

// Validate reports whether c has all required fields set. Mount calls
// this before installing any routes; exposed here for callers that
// compose Config across layers and want to check independently.
func (c *Config) Validate() error {
	if c.Store == nil {
		return errors.New("server: Config.Store is required")
	}
	if c.ManagerBotUsername == "" {
		return errors.New("server: Config.ManagerBotUsername is required")
	}
	if c.NoncePrefix == "" {
		return errors.New("server: Config.NoncePrefix is required")
	}
	if c.Authenticator == nil {
		return errors.New("server: Config.Authenticator is required (use BearerAuth for shared-secret)")
	}
	return nil
}

// effectiveTTL returns PairingTTL or [DefaultPairingTTL] when unset.
func (c *Config) effectiveTTL() time.Duration {
	if c.PairingTTL > 0 {
		return c.PairingTTL
	}
	return DefaultPairingTTL
}

// BearerAuth returns an [Authenticator] that accepts requests carrying
// an exact "Authorization: Bearer <secret>" header. Convenience for the
// common case; callers with richer requirements (HMAC signatures,
// Origin checks, mTLS, IP allowlists) should write their own.
func BearerAuth(secret string) func(*http.Request) bool {
	expected := "Bearer " + secret
	return func(r *http.Request) bool {
		return r.Header.Get("Authorization") == expected
	}
}

// Mount installs the three pairing routes on mux under prefix. prefix
// must not end with a slash; pass "" to mount at root. Returns an
// error if Config is invalid — in that case nothing is registered on
// the mux.
//
// Routes installed:
//
//	POST <prefix>/pair
//	PUT  <prefix>/pair/{nonce}
//	GET  <prefix>/pair/{nonce}
//
// The handlers use Go 1.22+ ServeMux pattern syntax ({nonce} + method
// prefix); Mount is a no-op on earlier runtimes.
func Mount(mux *http.ServeMux, cfg Config, prefix string) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	prefix = strings.TrimRight(prefix, "/")
	if prefix != "" && !strings.HasPrefix(prefix, "/") {
		return fmt.Errorf("server: prefix must start with '/' or be empty (got %q)", prefix)
	}
	mux.HandleFunc("POST "+prefix+"/pair", PostPair(cfg))
	mux.HandleFunc("PUT "+prefix+"/pair/{nonce}", PutPair(cfg))
	mux.HandleFunc("GET "+prefix+"/pair/{nonce}", GetPair(cfg))
	return nil
}
