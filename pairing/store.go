// Package pairing implements the nonce-based token handoff protocol used
// to deliver a newly-created Managed Bot's token from the manager bot
// runtime to the client session that initiated the creation.
//
// Pattern (after [hermes-agent]):
//
//  1. Client generates a nonce and calls Store.Put(ctx, nonce, ttl),
//     then displays a deep link with the nonce embedded in the suggested
//     child bot username.
//  2. User taps the link, confirms in Telegram. Manager bot receives a
//     [managed_bot_created] update, extracts the nonce from the username,
//     fetches the child token via getManagedBotToken, and calls
//     Store.Complete(ctx, nonce, token, username).
//  3. Client polls Store.FetchAndDelete(ctx, nonce) until it returns a
//     non-nil [*Entry]. On success the entry is atomically deleted — the
//     token is one-time-retrievable.
//
// Implementations MUST make FetchAndDelete atomic to prevent double-use.
// The package ships an in-memory implementation for tests; production
// users should back the Store with Redis (see examples/).
//
// [hermes-agent]: https://github.com/NousResearch/hermes-agent/issues/10591
// [managed_bot_created]: https://core.telegram.org/bots/api#managedbotcreated
package pairing

import (
	"context"
	"errors"
	"time"
)

// Status represents the pairing lifecycle state.
type Status string

const (
	// StatusWaiting is the initial state: client has registered a nonce
	// but the manager bot has not yet completed it with a token.
	StatusWaiting Status = "waiting"

	// StatusReady means the manager bot has completed the pairing; the
	// client can now fetch the token via [Store.FetchAndDelete].
	StatusReady Status = "ready"
)

// Entry is returned from [Store.FetchAndDelete]. Only set once the
// pairing is Ready.
type Entry struct {
	Nonce       string
	Token       string
	BotUsername string
	CompletedAt time.Time
}

// Sentinel errors returned by [Store] implementations.
var (
	// ErrNotFound indicates the nonce was never registered or has expired.
	ErrNotFound = errors.New("pairing: nonce not found")

	// ErrNotReady indicates the nonce exists but the manager bot has not
	// yet completed it. Clients should continue polling.
	ErrNotReady = errors.New("pairing: nonce not ready")

	// ErrAlreadyExists is returned by Put if the nonce collides with an
	// existing entry. Callers should regenerate and retry.
	ErrAlreadyExists = errors.New("pairing: nonce already exists")

	// ErrInvalidState is returned by Complete if the entry is not in
	// StatusWaiting (e.g., already completed or expired).
	ErrInvalidState = errors.New("pairing: invalid state for completion")
)

// Store is the pairing state abstraction. Implementations back this with
// Redis, Cloudflare KV, Postgres, or in-memory (for tests).
type Store interface {
	// Put registers a new nonce in StatusWaiting with the given TTL.
	// Returns ErrAlreadyExists if the nonce is already in use.
	Put(ctx context.Context, nonce string, ttl time.Duration) error

	// Complete transitions a StatusWaiting entry to StatusReady with the
	// given token and bot username. Returns ErrNotFound if the nonce
	// does not exist, ErrInvalidState if the entry is not Waiting.
	Complete(ctx context.Context, nonce, token, botUsername string) error

	// FetchAndDelete atomically retrieves a StatusReady entry and deletes
	// it, returning the token and username to the caller. Returns
	// ErrNotReady if the entry exists but is still Waiting, ErrNotFound
	// otherwise.
	//
	// This method MUST be atomic: concurrent calls with the same nonce
	// must return exactly one success and the rest must return ErrNotFound.
	FetchAndDelete(ctx context.Context, nonce string) (*Entry, error)
}
