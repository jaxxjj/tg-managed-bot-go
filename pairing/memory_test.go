package pairing

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMemoryStore_Lifecycle(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	if err := s.Put(ctx, "abc", time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Before Complete, FetchAndDelete should return ErrNotReady.
	_, err := s.FetchAndDelete(ctx, "abc")
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("FetchAndDelete before Complete: want ErrNotReady, got %v", err)
	}

	if err := s.Complete(ctx, "abc", "123:TOKEN", "alva_abc_bot"); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	entry, err := s.FetchAndDelete(ctx, "abc")
	if err != nil {
		t.Fatalf("FetchAndDelete: %v", err)
	}
	if entry.Token != "123:TOKEN" || entry.BotUsername != "alva_abc_bot" {
		t.Errorf("unexpected entry: %+v", entry)
	}

	// Second fetch should miss (one-time use).
	_, err = s.FetchAndDelete(ctx, "abc")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("second FetchAndDelete: want ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_DuplicatePut(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	if err := s.Put(ctx, "n", time.Minute); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	err := s.Put(ctx, "n", time.Minute)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("second Put: want ErrAlreadyExists, got %v", err)
	}
}

func TestMemoryStore_CompleteMissing(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	err := s.Complete(ctx, "nope", "t", "u")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_DoubleComplete(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	_ = s.Put(ctx, "n", time.Minute)
	_ = s.Complete(ctx, "n", "t", "u")
	err := s.Complete(ctx, "n", "t2", "u2")
	if !errors.Is(err, ErrInvalidState) {
		t.Errorf("want ErrInvalidState, got %v", err)
	}
}

func TestMemoryStore_Expiration(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	if err := s.Put(ctx, "temp", 10*time.Millisecond); err != nil {
		t.Fatalf("Put: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	// Expired; Complete should miss.
	err := s.Complete(ctx, "temp", "t", "u")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expired Complete: want ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_ContextCancelled(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	s := NewMemoryStore()

	if err := s.Put(cancelled, "n", time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("Put on cancelled ctx: got %v, want context.Canceled", err)
	}
	if err := s.Complete(cancelled, "n", "t", "u"); !errors.Is(err, context.Canceled) {
		t.Errorf("Complete on cancelled ctx: got %v, want context.Canceled", err)
	}
	if _, err := s.FetchAndDelete(cancelled, "n"); !errors.Is(err, context.Canceled) {
		t.Errorf("FetchAndDelete on cancelled ctx: got %v, want context.Canceled", err)
	}
}

func TestMemoryStore_Len(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore().(interface{ Len() int })

	if s.Len() != 0 {
		t.Errorf("initial Len = %d, want 0", s.Len())
	}

	store := s.(Store)
	_ = store.Put(ctx, "a", time.Minute)
	_ = store.Put(ctx, "b", time.Minute)
	if s.Len() != 2 {
		t.Errorf("after 2 Put: Len = %d, want 2", s.Len())
	}

	_ = store.Put(ctx, "c", 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	// Expired entry gets swept on next Len() call.
	if s.Len() != 2 {
		t.Errorf("after sweep: Len = %d, want 2", s.Len())
	}
}

func TestMemoryStore_ConcurrentFetch(t *testing.T) {
	// Verify only one goroutine gets the entry (atomicity).
	ctx := context.Background()
	s := NewMemoryStore()
	_ = s.Put(ctx, "race", time.Minute)
	_ = s.Complete(ctx, "race", "TOKEN", "u")

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
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrNotFound) {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Errorf("want exactly 1 success, got %d", successes)
	}
}
