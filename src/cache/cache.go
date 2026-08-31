// Package cache provides cashp's pluggable cache layer per AI.md PART 9:
// an in-process memory backend for development and single-instance
// deployments, and a Valkey/Redis backend (RESP over TCP, no third-party
// driver) for production and cluster mode.
//
// Keys follow the hierarchical naming rules from PART 9 — colon
// separated, lowercase, no spaces — and invalidation supports the
// time-based, event-based, and version-based strategies described there.
package cache

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Supported cache driver names, as accepted by server.yml.
const (
	// DriverMemory is the in-process backend: default, lost on restart.
	DriverMemory = "memory"
	// DriverValkey is the preferred production backend.
	DriverValkey = "valkey"
	// DriverRedis speaks the same protocol as DriverValkey.
	DriverRedis = "redis"
)

// Default TTLs from the cache TTL table in AI.md PART 9. A TTL of zero
// means "no expiry" and is only correct for entries revoked explicitly.
const (
	// TTLSession matches the session.timeout default.
	TTLSession = 24 * time.Hour
	// TTLAPIToken never expires — API tokens are revoked explicitly.
	TTLAPIToken = time.Duration(0)
	// TTLRateLimit is the rolling rate limit window.
	TTLRateLimit = time.Minute
	// TTLUserProfile balances freshness against database load.
	TTLUserProfile = 5 * time.Minute
	// TTLConfig keeps configuration changes propagating quickly.
	TTLConfig = time.Minute
	// TTLStaticHash caches immutable static content hashes.
	TTLStaticHash = 24 * time.Hour
	// TTLGeoIP caches GeoIP metadata, which updates infrequently.
	TTLGeoIP = 7 * 24 * time.Hour
	// TTLBlocklist keeps security blocklists reasonably fresh.
	TTLBlocklist = time.Hour
	// TTLPage caches rendered dynamic pages.
	TTLPage = 5 * time.Minute
	// TTLAPIResponse caches frequently changing API responses.
	TTLAPIResponse = 30 * time.Second
)

// Sentinel errors returned by every backend.
var (
	// ErrClosed is returned once Close has been called on a cache.
	ErrClosed = errors.New("cache: closed")
	// ErrEmptyKey is returned for an empty key, which no backend accepts.
	ErrEmptyKey = errors.New("cache: empty key")
	// ErrEmptyPrefix guards DeletePrefix against wiping the entire cache.
	ErrEmptyPrefix = errors.New("cache: empty prefix")
	// ErrUnsupported is returned when a backend lacks an optional feature.
	ErrUnsupported = errors.New("cache: operation not supported by this backend")
	// ErrUnknownDriver is returned by New for an unrecognized driver name.
	ErrUnknownDriver = errors.New("cache: unknown driver")
	// ErrLockTTL is returned when a lock is requested without a TTL, which
	// would leave the lock held forever if the owner died.
	ErrLockTTL = errors.New("cache: lock ttl must be positive")
)

// Cache is the backend-independent cache contract. Implementations are
// safe for concurrent use by multiple goroutines.
type Cache interface {
	// Get returns the cached value and whether it was present. A missing
	// or expired key is not an error.
	Get(ctx context.Context, key string) ([]byte, bool, error)
	// Set stores val under key. A ttl of zero or less stores the entry
	// without expiry.
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	// Delete removes a single key. Removing a missing key is not an error.
	Delete(ctx context.Context, key string) error
	// DeletePrefix removes every key starting with prefix. An empty
	// prefix returns ErrEmptyPrefix rather than clearing the cache.
	DeletePrefix(ctx context.Context, prefix string) error
	// Close releases the backend's resources. It is idempotent.
	Close() error
}

// Locker is implemented by backends that support atomic
// set-if-not-exists, the primitive behind the distributed locks in AI.md
// PART 9. Both the memory and Valkey backends implement it.
type Locker interface {
	// SetNX stores val under key only if key does not already exist and
	// reports whether the value was stored.
	SetNX(ctx context.Context, key string, val []byte, ttl time.Duration) (bool, error)
}

// Options configures a cache backend. Zero values select the documented
// defaults, so an empty Options is valid for local development.
type Options struct {
	// Driver is one of DriverMemory, DriverValkey, or DriverRedis.
	Driver string
	// MaxEntries bounds the memory backend; ignored by other backends.
	MaxEntries int
	// Addr is the "host:port" of the Valkey/Redis server.
	Addr string
	// Username is the ACL user for Valkey/Redis 6+ (optional).
	Username string
	// Password authenticates the connection; it is never logged.
	Password string
	// DB selects the logical database number.
	DB int
	// PoolSize bounds the number of idle pooled connections.
	PoolSize int
	// DialTimeout bounds connection establishment.
	DialTimeout time.Duration
	// Timeout bounds a single command round trip.
	Timeout time.Duration
	// KeyPrefix namespaces every key so instances can share a server.
	KeyPrefix string
}

// New builds the cache backend named by opts.Driver. An empty driver
// selects the memory backend, matching the server.yml default.
func New(opts Options) (Cache, error) {
	switch strings.ToLower(strings.TrimSpace(opts.Driver)) {
	case "", DriverMemory:
		return NewMemory(opts.MaxEntries), nil
	case DriverValkey, DriverRedis:
		return NewValkey(opts)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownDriver, opts.Driver)
	}
}

// Key builds a hierarchical cache key from its segments, applying the key
// naming rules from AI.md PART 9: colon separators, lowercase, no spaces
// or special characters. Empty segments are dropped.
//
// Segments must never be raw secrets: tokens are stored and keyed by
// their SHA-256 hex digest, never their plaintext.
func Key(parts ...string) string {
	segments := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := normalizeSegment(p); s != "" {
			segments = append(segments, s)
		}
	}
	return strings.Join(segments, ":")
}

// VersionedKey prefixes a key with a cache-busting version segment, the
// version-based invalidation strategy from AI.md PART 9. Old versions are
// never read again and fall out of the cache through their TTL.
func VersionedKey(version int, parts ...string) string {
	return Key(append([]string{"v" + strconv.Itoa(version)}, parts...)...)
}

// RateKey builds a rate limiting key, e.g. rate:api:192.168.1.1.
func RateKey(kind, subject string) string {
	return Key("rate", kind, subject)
}

// LockKey builds a distributed lock key, e.g. lock:backup.
func LockKey(resource string) string {
	return Key("lock", resource)
}

// UserKey builds a user-scoped key, e.g. user:123:sessions.
func UserKey(userID any, parts ...string) string {
	return Key(append([]string{"user", fmt.Sprintf("%v", userID)}, parts...)...)
}

// GetOrSet returns the cached value for key, loading and caching it on a
// miss. A backend read failure is not fatal — it falls through to load.
//
// When the load succeeds but the cache write fails, the loaded value is
// returned together with the write error: the value is still valid and
// callers that only need the data may use it and log the error.
//
// A nil load function returns ErrUnsupported — there is nothing to fall
// back to on a miss.
func GetOrSet(ctx context.Context, c Cache, key string, ttl time.Duration, load func(context.Context) ([]byte, error)) ([]byte, error) {
	if load == nil {
		return nil, ErrUnsupported
	}
	if c != nil {
		if val, ok, err := c.Get(ctx, key); err == nil && ok {
			return val, nil
		}
	}

	val, err := load(ctx)
	if err != nil {
		return nil, err
	}
	if c != nil {
		if err := c.Set(ctx, key, val, ttl); err != nil {
			return val, err
		}
	}
	return val, nil
}

// InvalidateScope removes an exact key and every key nested beneath it,
// the event-based invalidation strategy from AI.md PART 9. The nested
// sweep uses the trailing separator so scope "user:1" never matches the
// unrelated "user:12".
func InvalidateScope(ctx context.Context, c Cache, scope string) error {
	if c == nil {
		return nil
	}
	if scope == "" {
		return ErrEmptyPrefix
	}
	if err := c.Delete(ctx, scope); err != nil {
		return err
	}
	return c.DeletePrefix(ctx, scope+":")
}

// InvalidateUser removes every cache entry belonging to a user, which is
// required on every write to that user (AI.md PART 9: delete all
// user:{id}* keys on write).
func InvalidateUser(ctx context.Context, c Cache, userID any) error {
	return InvalidateScope(ctx, c, UserKey(userID))
}

// AcquireLock takes a distributed lock on a resource for at most ttl,
// reporting whether this node won it. Nodes that lose the race skip the
// work; the TTL guarantees the lock is released even if the owner dies.
func AcquireLock(ctx context.Context, c Cache, resource, owner string, ttl time.Duration) (bool, error) {
	locker, ok := c.(Locker)
	if !ok {
		return false, ErrUnsupported
	}
	if ttl <= 0 {
		return false, ErrLockTTL
	}
	return locker.SetNX(ctx, LockKey(resource), []byte(owner), ttl)
}

// ReleaseLock releases a lock only when this node still owns it, so a
// lock that already expired and was taken by another node is left alone.
// The ownership check and the delete are separate operations: an owner
// that stalls past the TTL may still delete a lock it no longer holds,
// which is why every lock TTL must exceed the guarded work's duration.
func ReleaseLock(ctx context.Context, c Cache, resource, owner string) error {
	if c == nil {
		return nil
	}
	key := LockKey(resource)
	val, ok, err := c.Get(ctx, key)
	if err != nil {
		return err
	}
	if !ok || subtle.ConstantTimeCompare(val, []byte(owner)) != 1 {
		return nil
	}
	return c.Delete(ctx, key)
}

// normalizeSegment lowercases a key segment and replaces every character
// outside the allowed set with a hyphen, so no caller-supplied value can
// inject a separator or a glob metacharacter into a key.
func normalizeSegment(part string) string {
	s := strings.ToLower(strings.TrimSpace(part))
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '_' || r == '-' || r == '=':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
