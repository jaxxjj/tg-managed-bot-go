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
