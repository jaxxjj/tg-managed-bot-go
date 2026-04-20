package pairing

import (
	"context"
	"sync"
	"time"
)

// NewMemoryStore returns an in-process [Store] suitable for tests and
// local development. It is NOT suitable for production multi-instance
// deployments because state is not shared across processes.
func NewMemoryStore() Store {
	return &memoryStore{
		entries: make(map[string]*memoryEntry),
	}
}

type memoryEntry struct {
	status      Status
	token       string
	botUsername string
	expiresAt   time.Time
	completedAt time.Time
}

type memoryStore struct {
	mu      sync.Mutex
	entries map[string]*memoryEntry
}

func (s *memoryStore) Put(ctx context.Context, nonce string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweepLocked()

	if _, ok := s.entries[nonce]; ok {
		return ErrAlreadyExists
	}
	s.entries[nonce] = &memoryEntry{
		status:    StatusWaiting,
		expiresAt: time.Now().Add(ttl),
	}
	return nil
}

func (s *memoryStore) Complete(ctx context.Context, nonce, token, botUsername string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweepLocked()

	e, ok := s.entries[nonce]
	if !ok {
		return ErrNotFound
	}
	if e.status != StatusWaiting {
		return ErrInvalidState
	}
	e.status = StatusReady
	e.token = token
	e.botUsername = botUsername
	e.completedAt = time.Now()
	return nil
}

func (s *memoryStore) FetchAndDelete(ctx context.Context, nonce string) (*Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweepLocked()

	e, ok := s.entries[nonce]
	if !ok {
		return nil, ErrNotFound
	}
	if e.status != StatusReady {
		return nil, ErrNotReady
	}
	out := &Entry{
		Nonce:       nonce,
		Token:       e.token,
		BotUsername: e.botUsername,
		CompletedAt: e.completedAt,
	}
	delete(s.entries, nonce)
	return out, nil
}

// sweepLocked removes expired entries. Caller must hold s.mu.
//
// Note: this runs O(N) every call. For the expected use case (short TTL,
// low concurrent-pairing count) this is fine. Production deployments
// should use a Store backed by a system with native TTL (Redis EXPIRE,
// Cloudflare KV expiration, Postgres partial indexes with a cron).
func (s *memoryStore) sweepLocked() {
	now := time.Now()
	for nonce, e := range s.entries {
		if now.After(e.expiresAt) {
			delete(s.entries, nonce)
		}
	}
}

// Len returns the number of entries currently held, including Waiting and
// Ready states. Exposed on the concrete type (not the [Store] interface)
// for tests and local observability.
func (s *memoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	return len(s.entries)
}
