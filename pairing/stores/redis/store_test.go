package redisstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/alva-ai/tg-managed-bot-go/pairing"
)

// newTestStore returns (store, miniredis) using a fresh miniredis
// instance. Both are cleaned up at end of test.
func newTestStore(t *testing.T, opts ...Option) (*Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewStore(rdb, opts...), mr
}

func TestStore_Lifecycle(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)

	const n = "abcdef123456"

	// 1. Before Put, Complete should ErrNotFound.
	if err := s.Complete(ctx, n, "tok", "u_bot"); !errors.Is(err, pairing.ErrNotFound) {
		t.Errorf("Complete before Put: %v, want ErrNotFound", err)
	}

	// 2. Put.
	if err := s.Put(ctx, n, 30*time.Second); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// 3. FetchAndDelete in Waiting state → ErrNotReady.
	if _, err := s.FetchAndDelete(ctx, n); !errors.Is(err, pairing.ErrNotReady) {
		t.Errorf("FetchAndDelete Waiting: %v, want ErrNotReady", err)
	}

	// 4. Complete.
	if err := s.Complete(ctx, n, "tok-123", "alva_x_bot"); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// 5. FetchAndDelete → entry + delete.
	e, err := s.FetchAndDelete(ctx, n)
	if err != nil {
		t.Fatalf("FetchAndDelete: %v", err)
	}
	if e.Token != "tok-123" || e.BotUsername != "alva_x_bot" {
		t.Errorf("unexpected entry: %+v", e)
	}
	if e.CompletedAt.IsZero() {
		t.Errorf("CompletedAt zero")
	}

	// 6. One-time: second FetchAndDelete → ErrNotFound.
	if _, err := s.FetchAndDelete(ctx, n); !errors.Is(err, pairing.ErrNotFound) {
		t.Errorf("second Fetch: %v, want ErrNotFound", err)
	}
}

func TestStore_DuplicatePut(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)

	if err := s.Put(ctx, "dup", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "dup", time.Minute); !errors.Is(err, pairing.ErrAlreadyExists) {
		t.Errorf("second Put: %v, want ErrAlreadyExists", err)
	}
}

func TestStore_DoubleComplete(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)

	_ = s.Put(ctx, "n", time.Minute)
	if err := s.Complete(ctx, "n", "t1", "u1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, "n", "t2", "u2"); !errors.Is(err, pairing.ErrInvalidState) {
		t.Errorf("second Complete: %v, want ErrInvalidState", err)
	}
}

func TestStore_Expiration(t *testing.T) {
	ctx := context.Background()
	s, mr := newTestStore(t)

	if err := s.Put(ctx, "temp", 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// Advance miniredis's clock past the TTL.
	mr.FastForward(200 * time.Millisecond)

	// Complete on an expired entry → ErrNotFound.
	if err := s.Complete(ctx, "temp", "t", "u"); !errors.Is(err, pairing.ErrNotFound) {
		t.Errorf("expired Complete: %v, want ErrNotFound", err)
	}

	// Also verify the key is truly gone.
	if mr.Exists(DefaultKeyPrefix + "temp") {
		t.Errorf("key should have expired")
	}
}

func TestStore_KeyPrefix(t *testing.T) {
	ctx := context.Background()
	s, mr := newTestStore(t, WithKeyPrefix("myapp:"))

	if err := s.Put(ctx, "n1", time.Minute); err != nil {
		t.Fatal(err)
	}
	if !mr.Exists("myapp:n1") {
		t.Errorf("key with custom prefix missing; keys = %v", mr.Keys())
	}
	if mr.Exists(DefaultKeyPrefix + "n1") {
		t.Errorf("default-prefix key should not exist under custom-prefix store")
	}
}

func TestStore_ContextCancelled(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	s, _ := newTestStore(t)

	if err := s.Put(cancelled, "n", time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("Put cancelled ctx: %v, want context.Canceled", err)
	}
	if err := s.Complete(cancelled, "n", "t", "u"); !errors.Is(err, context.Canceled) {
		t.Errorf("Complete cancelled ctx: %v, want context.Canceled", err)
	}
	if _, err := s.FetchAndDelete(cancelled, "n"); !errors.Is(err, context.Canceled) {
		t.Errorf("FetchAndDelete cancelled ctx: %v, want context.Canceled", err)
	}
}

func TestStore_ConcurrentFetch(t *testing.T) {
	// Lua atomicity: N goroutines, exactly 1 success, others ErrNotFound.
	ctx := context.Background()
	s, _ := newTestStore(t)

	_ = s.Put(ctx, "race", time.Minute)
	_ = s.Complete(ctx, "race", "THE_TOKEN", "alva_race_bot")

	const N = 50
	var wg sync.WaitGroup
	results := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.FetchAndDelete(ctx, "race")
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	var successes int
	var notFounds int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, pairing.ErrNotFound):
			notFounds++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Errorf("expected exactly 1 success, got %d", successes)
	}
	if notFounds != N-1 {
		t.Errorf("expected %d ErrNotFound, got %d", N-1, notFounds)
	}
}

func TestStore_NewStore_Defaults(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	s := NewStore(rdb)
	if s.prefix != DefaultKeyPrefix {
		t.Errorf("prefix = %q, want %q", s.prefix, DefaultKeyPrefix)
	}
	if s.scriptPut == nil || s.scriptComplete == nil || s.scriptFetchAndDelete == nil {
		t.Errorf("scripts not initialized")
	}
}
