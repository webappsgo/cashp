package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestMemorySetGetDelete(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(8)
	defer func() { _ = c.Close() }()

	if _, ok, err := c.Get(ctx, "user:1"); err != nil || ok {
		t.Fatalf("miss expected: ok = %v, err = %v", ok, err)
	}

	if err := c.Set(ctx, "user:1", []byte("alice"), 0); err != nil {
		t.Fatalf("set: %v", err)
	}
	val, ok, err := c.Get(ctx, "user:1")
	if err != nil || !ok || string(val) != "alice" {
		t.Fatalf("get = %q, %v, %v", val, ok, err)
	}

	if err := c.Delete(ctx, "user:1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := c.Get(ctx, "user:1"); ok {
		t.Fatal("deleted key still present")
	}
	if err := c.Delete(ctx, "user:1"); err != nil {
		t.Fatalf("deleting a missing key must not error: %v", err)
	}
}

func TestMemoryStoresCopies(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(4)
	defer func() { _ = c.Close() }()

	input := []byte("alice")
	if err := c.Set(ctx, "user:1", input, 0); err != nil {
		t.Fatalf("set: %v", err)
	}
	input[0] = 'X'

	val, _, _ := c.Get(ctx, "user:1")
	if string(val) != "alice" {
		t.Fatalf("caller mutation leaked into the cache: %q", val)
	}

	val[0] = 'Y'
	again, _, _ := c.Get(ctx, "user:1")
	if string(again) != "alice" {
		t.Fatalf("returned slice aliases the stored value: %q", again)
	}
}

func TestMemoryTTLExpiry(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(4)
	defer func() { _ = c.Close() }()

	if err := c.Set(ctx, "rate:api:1.2.3.4", []byte("1"), 10*time.Millisecond); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, ok, _ := c.Get(ctx, "rate:api:1.2.3.4"); !ok {
		t.Fatal("value should be present before expiry")
	}

	time.Sleep(25 * time.Millisecond)
	if _, ok, _ := c.Get(ctx, "rate:api:1.2.3.4"); ok {
		t.Fatal("expired value must not be returned")
	}
}

func TestMemoryJanitorSweepsExpiredEntries(t *testing.T) {
	ctx := context.Background()
	m := newMemory(16, 5*time.Millisecond)
	defer func() { _ = m.Close() }()

	if err := m.Set(ctx, "page:home", []byte("x"), 5*time.Millisecond); err != nil {
		t.Fatalf("set: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m.Len() == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("janitor did not reclaim the expired entry")
}

func TestMemoryEvictsLeastRecentlyUsed(t *testing.T) {
	ctx := context.Background()
	m := newMemory(3, time.Hour)
	defer func() { _ = m.Close() }()

	for i := 1; i <= 3; i++ {
		if err := m.Set(ctx, fmt.Sprintf("user:%d", i), []byte("x"), 0); err != nil {
			t.Fatalf("set: %v", err)
		}
	}

	if _, ok, _ := m.Get(ctx, "user:1"); !ok {
		t.Fatal("user:1 should still be cached")
	}

	if err := m.Set(ctx, "user:4", []byte("x"), 0); err != nil {
		t.Fatalf("set: %v", err)
	}

	if m.Len() != 3 {
		t.Fatalf("len = %d, want 3", m.Len())
	}
	if _, ok, _ := m.Get(ctx, "user:2"); ok {
		t.Fatal("least recently used entry should have been evicted")
	}
	for _, k := range []string{"user:1", "user:3", "user:4"} {
		if _, ok, _ := m.Get(ctx, k); !ok {
			t.Errorf("%s should still be cached", k)
		}
	}
}

func TestMemoryReplaceKeepsSizeBounded(t *testing.T) {
	ctx := context.Background()
	m := newMemory(2, time.Hour)
	defer func() { _ = m.Close() }()

	for i := 0; i < 5; i++ {
		if err := m.Set(ctx, "config:server", []byte(fmt.Sprint(i)), 0); err != nil {
			t.Fatalf("set: %v", err)
		}
	}
	if m.Len() != 1 {
		t.Fatalf("len = %d, want 1", m.Len())
	}
	val, _, _ := m.Get(ctx, "config:server")
	if string(val) != "4" {
		t.Fatalf("val = %q, want the latest write", val)
	}
}

func TestMemoryDeletePrefix(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(16)
	defer func() { _ = c.Close() }()

	for _, k := range []string{"user:1:a", "user:1:b", "user:2:a"} {
		if err := c.Set(ctx, k, []byte("x"), 0); err != nil {
			t.Fatalf("set: %v", err)
		}
	}

	if err := c.DeletePrefix(ctx, "user:1:"); err != nil {
		t.Fatalf("delete prefix: %v", err)
	}
	if _, ok, _ := c.Get(ctx, "user:1:a"); ok {
		t.Fatal("prefix delete missed a key")
	}
	if _, ok, _ := c.Get(ctx, "user:2:a"); !ok {
		t.Fatal("prefix delete removed an unrelated key")
	}

	if err := c.DeletePrefix(ctx, ""); !errors.Is(err, ErrEmptyPrefix) {
		t.Fatalf("err = %v, want ErrEmptyPrefix", err)
	}
}

func TestMemorySetNX(t *testing.T) {
	ctx := context.Background()
	m := newMemory(8, time.Hour)
	defer func() { _ = m.Close() }()

	ok, err := m.SetNX(ctx, "lock:backup", []byte("node-a"), 20*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("first SetNX: ok = %v, err = %v", ok, err)
	}

	ok, err = m.SetNX(ctx, "lock:backup", []byte("node-b"), time.Minute)
	if err != nil {
		t.Fatalf("second SetNX: %v", err)
	}
	if ok {
		t.Fatal("SetNX must not overwrite a live key")
	}

	time.Sleep(30 * time.Millisecond)
	ok, err = m.SetNX(ctx, "lock:backup", []byte("node-b"), time.Minute)
	if err != nil || !ok {
		t.Fatalf("SetNX after expiry: ok = %v, err = %v", ok, err)
	}
	val, _, _ := m.Get(ctx, "lock:backup")
	if string(val) != "node-b" {
		t.Fatalf("val = %q", val)
	}
}

func TestMemoryEmptyKeyRejected(t *testing.T) {
	ctx := context.Background()
	m := newMemory(4, time.Hour)
	defer func() { _ = m.Close() }()

	if _, _, err := m.Get(ctx, ""); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("Get: %v", err)
	}
	if err := m.Set(ctx, "", nil, 0); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("Set: %v", err)
	}
	if err := m.Delete(ctx, ""); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("Delete: %v", err)
	}
	if _, err := m.SetNX(ctx, "", nil, time.Minute); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("SetNX: %v", err)
	}
}

func TestMemoryClosed(t *testing.T) {
	ctx := context.Background()
	m := newMemory(4, time.Hour)

	if err := m.Set(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("close must be idempotent: %v", err)
	}

	if _, _, err := m.Get(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("Get: %v", err)
	}
	if err := m.Set(ctx, "k", []byte("v"), 0); !errors.Is(err, ErrClosed) {
		t.Errorf("Set: %v", err)
	}
	if err := m.Delete(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("Delete: %v", err)
	}
	if err := m.DeletePrefix(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("DeletePrefix: %v", err)
	}
	if _, err := m.SetNX(ctx, "k", []byte("v"), time.Minute); !errors.Is(err, ErrClosed) {
		t.Errorf("SetNX: %v", err)
	}
	if m.Len() != 0 {
		t.Errorf("close must release cached values, len = %d", m.Len())
	}
}

func TestMemoryHonorsCancelledContext(t *testing.T) {
	m := newMemory(4, time.Hour)
	defer func() { _ = m.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := m.Get(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Get: %v", err)
	}
	if err := m.Set(ctx, "k", nil, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("Set: %v", err)
	}
	if err := m.Delete(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete: %v", err)
	}
	if err := m.DeletePrefix(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("DeletePrefix: %v", err)
	}
	if _, err := m.SetNX(ctx, "k", nil, time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("SetNX: %v", err)
	}
}

func TestMemoryConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	m := newMemory(64, 5*time.Millisecond)
	defer func() { _ = m.Close() }()

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("user:%d:%d", worker, i%16)
				if err := m.Set(ctx, key, []byte("x"), time.Duration(i%3)*time.Millisecond); err != nil {
					t.Errorf("set: %v", err)
					return
				}
				if _, _, err := m.Get(ctx, key); err != nil {
					t.Errorf("get: %v", err)
					return
				}
				if _, err := m.SetNX(ctx, "lock:x", []byte("owner"), 2*time.Millisecond); err != nil {
					t.Errorf("setnx: %v", err)
					return
				}
				if err := m.Delete(ctx, key); err != nil {
					t.Errorf("delete: %v", err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
}

func TestNewMemoryUsesDefaultBound(t *testing.T) {
	m := newMemory(0, 0)
	defer func() { _ = m.Close() }()

	if m.maxEntries != DefaultMaxEntries {
		t.Fatalf("maxEntries = %d, want %d", m.maxEntries, DefaultMaxEntries)
	}
}
