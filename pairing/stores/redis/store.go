// Package redis provides a Redis-backed [pairing.Store] implementation
// built on [github.com/redis/go-redis/v9]. Atomic Put, Complete, and
// FetchAndDelete operations are implemented with server-side Lua
// scripts so concurrent callers cannot race on token delivery.
//
// This is a separate Go module from the root package: importing it
// adds go-redis (and its transitive deps) only to consumers that
// actually want a Redis-backed pairing service. Applications that stay
// on the in-memory Store pay zero.
//
// # Key layout
//
// Each pairing is one Redis hash:
//
//	pairing:<nonce>  HASH {
//	    status         "waiting" | "ready"
//	    token          set only after Complete
//	    bot_username   set only after Complete
//	    completed_at   Unix seconds (string), set only after Complete
//	}
//
// TTL is applied on the hash key at Put via PEXPIRE; Redis expires the
// whole hash atomically once the TTL elapses, so partial state is
// impossible.
//
// # Example
//
//	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
//	store := redisstore.NewStore(rdb, redisstore.WithKeyPrefix("alva:pairing:"))
package redisstore

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/alva-ai/tg-managed-bot-go/pairing"
)

// DefaultKeyPrefix is prepended to every nonce to form the Redis key.
// Override with [WithKeyPrefix] when cohabiting with other consumers.
const DefaultKeyPrefix = "pairing:"

// Lua scripts are embedded as plain text so they stay diffable and
// syntax-highlighted in the source tree.
var (
	//go:embed put.lua
	putLua string
	//go:embed complete.lua
	completeLua string
	//go:embed fetch_and_delete.lua
	fetchAndDeleteLua string
)

// Store implements [pairing.Store] backed by Redis.
//
// A Store is safe for concurrent use by multiple goroutines; the
// underlying *redis.Client owns its own connection pool.
type Store struct {
	rdb    redis.UniversalClient
	prefix string

	scriptPut            *redis.Script
	scriptComplete       *redis.Script
	scriptFetchAndDelete *redis.Script
}

// Option configures a [Store].
type Option func(*Store)

// WithKeyPrefix overrides [DefaultKeyPrefix]. The stored key is
// "<prefix><nonce>", so the prefix should typically end with a ':'.
func WithKeyPrefix(prefix string) Option {
	return func(s *Store) { s.prefix = prefix }
}

// NewStore constructs a [Store] from a go-redis client.
//
// The Lua scripts are parsed at construction time; their EVALSHA
// digests are cached by redis.Script.Run, so each operation is one
// round-trip on the happy path.
func NewStore(rdb redis.UniversalClient, opts ...Option) *Store {
	s := &Store{
		rdb:                  rdb,
		prefix:               DefaultKeyPrefix,
		scriptPut:            redis.NewScript(putLua),
		scriptComplete:       redis.NewScript(completeLua),
		scriptFetchAndDelete: redis.NewScript(fetchAndDeleteLua),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// key returns the Redis key for the given pairing nonce.
func (s *Store) key(nonce string) string {
	return s.prefix + nonce
}

// Put implements [pairing.Store].
func (s *Store) Put(ctx context.Context, nonce string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	res, err := s.scriptPut.Run(ctx, s.rdb, []string{s.key(nonce)}, ttl.Milliseconds()).Int64()
	if err != nil {
		return fmt.Errorf("redisstore: Put: %w", err)
	}
	switch res {
	case 1:
		return nil
	case 0:
		return pairing.ErrAlreadyExists
	default:
		return fmt.Errorf("redisstore: Put: unexpected script result %d", res)
	}
}

// Complete implements [pairing.Store].
func (s *Store) Complete(ctx context.Context, nonce, token, botUsername string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	now := time.Now().UTC().Unix()
	res, err := s.scriptComplete.Run(ctx, s.rdb,
		[]string{s.key(nonce)},
		token, botUsername, now,
	).Int64()
	if err != nil {
		return fmt.Errorf("redisstore: Complete: %w", err)
	}
	switch res {
	case 1:
		return nil
	case -1:
		return pairing.ErrNotFound
	case -2:
		return pairing.ErrInvalidState
	default:
		return fmt.Errorf("redisstore: Complete: unexpected script result %d", res)
	}
}

// FetchAndDelete implements [pairing.Store].
func (s *Store) FetchAndDelete(ctx context.Context, nonce string) (*pairing.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	raw, err := s.scriptFetchAndDelete.Run(ctx, s.rdb, []string{s.key(nonce)}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, pairing.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redisstore: FetchAndDelete: %w", err)
	}

	// Sentinel: -1 (int64) means Waiting.
	if n, ok := raw.(int64); ok && n == -1 {
		return nil, pairing.ErrNotReady
	}

	arr, ok := raw.([]any)
	if !ok || len(arr) != 3 {
		return nil, fmt.Errorf("redisstore: FetchAndDelete: unexpected script shape %T %v", raw, raw)
	}
	token, okT := arr[0].(string)
	username, okU := arr[1].(string)
	completedStr, okC := arr[2].(string)
	if !okT || !okU || !okC {
		return nil, fmt.Errorf(
			"redisstore: FetchAndDelete: unexpected field types (token=%T, username=%T, completed_at=%T)",
			arr[0], arr[1], arr[2],
		)
	}
	unix, err := strconv.ParseInt(completedStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("redisstore: FetchAndDelete: parse completed_at %q: %w", completedStr, err)
	}

	return &pairing.Entry{
		Nonce:       nonce,
		Token:       token,
		BotUsername: username,
		CompletedAt: time.Unix(unix, 0).UTC(),
	}, nil
}
