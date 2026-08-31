package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKeyNaming(t *testing.T) {
	cases := []struct {
		got  string
		want string
	}{
		{Key("user", "123"), "user:123"},
		{Key("user", "123", "sessions"), "user:123:sessions"},
		{Key("jokes", "list", "category=puns"), "jokes:list:category=puns"},
		{Key("org", "42", "settings"), "org:42:settings"},
		{Key("User", " 123 "), "user:123"},
		{Key("user", "", "profile"), "user:profile"},
		{Key("type", "a b"), "type:a-b"},
		{Key("type", "a:b"), "type:a-b"},
		{Key("type", "a*b?"), "type:a-b-"},
		{VersionedKey(1, "user", "123"), "v1:user:123"},
		{RateKey("api", "192.168.1.1"), "rate:api:192.168.1.1"},
		{LockKey("backup"), "lock:backup"},
		{UserKey(123), "user:123"},
		{UserKey("abc", "profile"), "user:abc:profile"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("key = %q, want %q", tc.got, tc.want)
		}
	}
}

func TestNewDriverSelection(t *testing.T) {
	c, err := New(Options{})
	if err != nil {
		t.Fatalf("default driver: %v", err)
	}
	if _, ok := c.(*memoryCache); !ok {
		t.Fatalf("default driver must be memory, got %T", c)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	c2, err := New(Options{Driver: DriverMemory, MaxEntries: 4})
	if err != nil {
		t.Fatalf("memory driver: %v", err)
	}
	if err := c2.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := New(Options{Driver: "memcached"}); !errors.Is(err, ErrUnknownDriver) {
		t.Fatalf("err = %v, want ErrUnknownDriver", err)
	}
}

func TestGetOrSet(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(8)
	defer func() { _ = c.Close() }()

	loads := 0
	load := func(context.Context) ([]byte, error) {
		loads++
		return []byte("value"), nil
	}

	for i := 0; i < 3; i++ {
		val, err := GetOrSet(ctx, c, "config:server", TTLConfig, load)
		if err != nil {
			t.Fatalf("GetOrSet: %v", err)
		}
		if string(val) != "value" {
			t.Fatalf("val = %q", val)
		}
	}
	if loads != 1 {
		t.Fatalf("loads = %d, want 1", loads)
	}
}

func TestGetOrSetLoadFailure(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(8)
	defer func() { _ = c.Close() }()

	want := errors.New("db down")
	if _, err := GetOrSet(ctx, c, "config:server", TTLConfig, func(context.Context) ([]byte, error) {
		return nil, want
	}); !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}

	if _, err := GetOrSet(ctx, c, "config:server", TTLConfig, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestGetOrSetSurfacesWriteFailure(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(8)
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	val, err := GetOrSet(ctx, c, "config:server", TTLConfig, func(context.Context) ([]byte, error) {
		return []byte("value"), nil
	})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	if string(val) != "value" {
		t.Fatalf("loaded value must still be returned, got %q", val)
	}
}

func TestGetOrSetWithNilCache(t *testing.T) {
	val, err := GetOrSet(context.Background(), nil, "k", TTLConfig, func(context.Context) ([]byte, error) {
		return []byte("v"), nil
	})
	if err != nil || string(val) != "v" {
		t.Fatalf("val = %q, err = %v", val, err)
	}
}

func TestInvalidateUser(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(16)
	defer func() { _ = c.Close() }()

	keys := []string{"user:1", "user:1:profile", "user:1:sessions", "user:12", "user:12:profile", "org:1"}
	for _, k := range keys {
		if err := c.Set(ctx, k, []byte("x"), 0); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}

	if err := InvalidateUser(ctx, c, 1); err != nil {
		t.Fatalf("InvalidateUser: %v", err)
	}

	gone := []string{"user:1", "user:1:profile", "user:1:sessions"}
	for _, k := range gone {
		if _, ok, _ := c.Get(ctx, k); ok {
			t.Errorf("%s should have been invalidated", k)
		}
	}
	kept := []string{"user:12", "user:12:profile", "org:1"}
	for _, k := range kept {
		if _, ok, _ := c.Get(ctx, k); !ok {
			t.Errorf("%s must not be invalidated by a user:1 write", k)
		}
	}
}

func TestInvalidateScopeGuards(t *testing.T) {
	ctx := context.Background()
	if err := InvalidateScope(ctx, nil, "user:1"); err != nil {
		t.Fatalf("nil cache: %v", err)
	}

	c := NewMemory(4)
	defer func() { _ = c.Close() }()
	if err := InvalidateScope(ctx, c, ""); !errors.Is(err, ErrEmptyPrefix) {
		t.Fatalf("err = %v, want ErrEmptyPrefix", err)
	}
}

func TestDistributedLock(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(8)
	defer func() { _ = c.Close() }()

	ok, err := AcquireLock(ctx, c, "backup", "node-a", time.Minute)
	if err != nil || !ok {
		t.Fatalf("first acquire: ok = %v, err = %v", ok, err)
	}

	ok, err = AcquireLock(ctx, c, "backup", "node-b", time.Minute)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if ok {
		t.Fatal("a held lock must not be granted twice")
	}

	if err := ReleaseLock(ctx, c, "backup", "node-b"); err != nil {
		t.Fatalf("release by non-owner: %v", err)
	}
	if _, held, _ := c.Get(ctx, LockKey("backup")); !held {
		t.Fatal("non-owner must not release the lock")
	}

	if err := ReleaseLock(ctx, c, "backup", "node-a"); err != nil {
		t.Fatalf("release by owner: %v", err)
	}
	if _, held, _ := c.Get(ctx, LockKey("backup")); held {
		t.Fatal("owner release must remove the lock")
	}

	if err := ReleaseLock(ctx, nil, "backup", "node-a"); err != nil {
		t.Fatalf("release with nil cache: %v", err)
	}
}

func TestAcquireLockRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(4)
	defer func() { _ = c.Close() }()

	if _, err := AcquireLock(ctx, c, "backup", "node-a", 0); err == nil {
		t.Fatal("a lock without a TTL must be rejected")
	}
	if _, err := AcquireLock(ctx, notALocker{}, "backup", "node-a", time.Minute); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

// notALocker is a Cache that deliberately does not implement Locker.
type notALocker struct{}

func (notALocker) Get(context.Context, string) ([]byte, bool, error) { return nil, false, nil }

func (notALocker) Set(context.Context, string, []byte, time.Duration) error { return nil }

func (notALocker) Delete(context.Context, string) error { return nil }

func (notALocker) DeletePrefix(context.Context, string) error { return nil }

func (notALocker) Close() error { return nil }

func TestTTLDefaults(t *testing.T) {
	if TTLSession != 24*time.Hour || TTLRateLimit != time.Minute || TTLAPIResponse != 30*time.Second {
		t.Fatal("TTL defaults must match the PART 9 table")
	}
	if TTLAPIToken != 0 {
		t.Fatal("API tokens expire by explicit revocation only")
	}
}
