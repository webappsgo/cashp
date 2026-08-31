package cache

import (
	"container/list"
	"context"
	"strings"
	"sync"
	"time"
)

// DefaultMaxEntries bounds the memory backend when the caller does not
// pick a limit, keeping a long-running process from growing without end.
const DefaultMaxEntries = 10000

// janitorInterval is how often expired entries are swept. Expired entries
// are also dropped lazily on read, so this only reclaims memory held by
// keys nobody reads again.
const janitorInterval = 30 * time.Second

// memoryEntry is one cached value plus its expiry and LRU position.
type memoryEntry struct {
	key       string
	val       []byte
	expiresAt time.Time
	elem      *list.Element
}

// expired reports whether the entry is past its expiry at time now. A
// zero expiresAt means the entry never expires.
func (e *memoryEntry) expired(now time.Time) bool {
	return !e.expiresAt.IsZero() && now.After(e.expiresAt)
}

// memoryCache is the in-process backend: goroutine-safe, TTL-expiring,
// and bounded by an LRU eviction policy.
type memoryCache struct {
	mu         sync.Mutex
	entries    map[string]*memoryEntry
	lru        *list.List
	maxEntries int
	closed     bool
	stop       chan struct{}
	done       chan struct{}
}

// NewMemory builds the in-process cache backend holding at most
// maxEntries values; a non-positive maxEntries uses DefaultMaxEntries.
// The returned cache runs a background janitor until Close is called.
func NewMemory(maxEntries int) Cache {
	return newMemory(maxEntries, janitorInterval)
}

// newMemory builds the memory backend with an explicit janitor interval
// so tests can sweep without waiting for the production interval.
func newMemory(maxEntries int, interval time.Duration) *memoryCache {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	if interval <= 0 {
		interval = janitorInterval
	}
	m := &memoryCache{
		entries:    make(map[string]*memoryEntry),
		lru:        list.New(),
		maxEntries: maxEntries,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	go m.janitor(interval)
	return m
}

// Get returns the value stored under key. An expired entry is dropped and
// reported as a miss.
func (m *memoryCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if key == "" {
		return nil, false, ErrEmptyKey
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, false, ErrClosed
	}

	e, ok := m.entries[key]
	if !ok {
		return nil, false, nil
	}
	if e.expired(time.Now()) {
		m.removeLocked(e)
		return nil, false, nil
	}

	m.lru.MoveToFront(e.elem)
	out := make([]byte, len(e.val))
	copy(out, e.val)
	return out, true, nil
}

// Set stores a copy of val under key, evicting the least recently used
// entry when the cache is full.
func (m *memoryCache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return ErrEmptyKey
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}

	m.storeLocked(key, val, ttl)
	return nil
}

// SetNX stores val only when key is absent or already expired, giving the
// memory backend the atomic primitive distributed locks need.
func (m *memoryCache) SetNX(ctx context.Context, key string, val []byte, ttl time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if key == "" {
		return false, ErrEmptyKey
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false, ErrClosed
	}

	if e, ok := m.entries[key]; ok {
		if !e.expired(time.Now()) {
			return false, nil
		}
		m.removeLocked(e)
	}

	m.storeLocked(key, val, ttl)
	return true, nil
}

// Delete removes key. Deleting a missing key is not an error.
func (m *memoryCache) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return ErrEmptyKey
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}

	if e, ok := m.entries[key]; ok {
		m.removeLocked(e)
	}
	return nil
}

// DeletePrefix removes every key starting with prefix.
func (m *memoryCache) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if prefix == "" {
		return ErrEmptyPrefix
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}

	for key, e := range m.entries {
		if strings.HasPrefix(key, prefix) {
			m.removeLocked(e)
		}
	}
	return nil
}

// Close stops the janitor and releases every cached value. It is safe to
// call more than once.
func (m *memoryCache) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	close(m.stop)
	m.mu.Unlock()

	<-m.done

	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = make(map[string]*memoryEntry)
	m.lru.Init()
	return nil
}

// Len reports how many entries are currently held, including ones that
// have expired but not yet been swept.
func (m *memoryCache) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

// storeLocked inserts or replaces an entry and enforces the size bound.
// The caller must hold m.mu.
func (m *memoryCache) storeLocked(key string, val []byte, ttl time.Duration) {
	stored := make([]byte, len(val))
	copy(stored, val)

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	if e, ok := m.entries[key]; ok {
		e.val = stored
		e.expiresAt = expiresAt
		m.lru.MoveToFront(e.elem)
		return
	}

	e := &memoryEntry{key: key, val: stored, expiresAt: expiresAt}
	e.elem = m.lru.PushFront(e)
	m.entries[key] = e

	for len(m.entries) > m.maxEntries {
		back := m.lru.Back()
		if back == nil {
			return
		}
		m.removeLocked(back.Value.(*memoryEntry))
	}
}

// removeLocked drops an entry from both the map and the LRU list. The
// caller must hold m.mu.
func (m *memoryCache) removeLocked(e *memoryEntry) {
	delete(m.entries, e.key)
	if e.elem != nil {
		m.lru.Remove(e.elem)
		e.elem = nil
	}
}

// janitor sweeps expired entries until Close signals it to stop.
func (m *memoryCache) janitor(interval time.Duration) {
	defer close(m.done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stop:
			return
		case now := <-ticker.C:
			m.sweep(now)
		}
	}
}

// sweep removes every entry that expired at or before now.
func (m *memoryCache) sweep(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}

	for _, e := range m.entries {
		if e.expired(now) {
			m.removeLocked(e)
		}
	}
}
