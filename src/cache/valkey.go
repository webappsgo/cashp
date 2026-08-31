package cache

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Valkey/Redis backend defaults, used for any Options field left zero.
const (
	defaultAddr        = "127.0.0.1:6379"
	defaultPoolSize    = 8
	defaultDialTimeout = 5 * time.Second
	defaultTimeout     = 5 * time.Second
	// scanBatch is the COUNT hint per SCAN round trip.
	scanBatch = 200
	// deleteBatch bounds how many keys one DEL command carries.
	deleteBatch = 200
)

// valkeyCache talks RESP to a Valkey or Redis server over a small pool of
// connections. It is safe for concurrent use.
type valkeyCache struct {
	opts   Options
	pool   chan *respConn
	mu     sync.Mutex
	closed bool
}

// NewValkey connects to a Valkey or Redis server and verifies the
// connection with a PING before returning, so a misconfigured cache fails
// at startup rather than on the first request.
func NewValkey(opts Options) (Cache, error) {
	opts = withCacheDefaults(opts)

	v := &valkeyCache{
		opts: opts,
		pool: make(chan *respConn, opts.PoolSize),
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.DialTimeout+opts.Timeout)
	defer cancel()

	conn, err := dialRESP(ctx, opts)
	if err != nil {
		return nil, err
	}
	if _, err := conn.do(ctx, opts.Timeout, cmd("PING")...); err != nil {
		conn.close()
		return nil, err
	}
	v.putConn(conn)

	return v, nil
}

// NewRedis connects to a Redis server. Redis and Valkey speak the same
// protocol, so this is NewValkey under the name the config uses.
func NewRedis(opts Options) (Cache, error) {
	return NewValkey(opts)
}

// withCacheDefaults fills in every unset connection option.
func withCacheDefaults(opts Options) Options {
	if strings.TrimSpace(opts.Addr) == "" {
		opts.Addr = defaultAddr
	}
	if opts.PoolSize <= 0 {
		opts.PoolSize = defaultPoolSize
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = defaultDialTimeout
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	return opts
}

// Get returns the value stored under key.
func (v *valkeyCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if key == "" {
		return nil, false, ErrEmptyKey
	}

	rp, err := v.do(ctx, cmd("GET", v.k(key))...)
	if err != nil {
		return nil, false, err
	}
	if rp.null {
		return nil, false, nil
	}
	return rp.str, true, nil
}

// Set stores val under key, with expiry when ttl is positive.
func (v *valkeyCache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if key == "" {
		return ErrEmptyKey
	}

	args := cmd("SET", v.k(key), val)
	if ms := ttlMillis(ttl); ms > 0 {
		args = append(args, cmd("PX", strconv.FormatInt(ms, 10))...)
	}
	_, err := v.do(ctx, args...)
	return err
}

// SetNX stores val only when key does not exist, the atomic primitive
// behind distributed locks. It is never retried: a retried SET NX after a
// network failure could report a lost race the caller actually won.
func (v *valkeyCache) SetNX(ctx context.Context, key string, val []byte, ttl time.Duration) (bool, error) {
	if key == "" {
		return false, ErrEmptyKey
	}

	args := cmd("SET", v.k(key), val)
	if ms := ttlMillis(ttl); ms > 0 {
		args = append(args, cmd("PX", strconv.FormatInt(ms, 10))...)
	}
	args = append(args, []byte("NX"))

	rp, err := v.doOnce(ctx, args...)
	if err != nil {
		return false, err
	}
	return !rp.null, nil
}

// Delete removes key.
func (v *valkeyCache) Delete(ctx context.Context, key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	_, err := v.do(ctx, cmd("DEL", v.k(key))...)
	return err
}

// DeletePrefix removes every key starting with prefix, walking the
// keyspace with SCAN so the server is never blocked by a KEYS sweep.
func (v *valkeyCache) DeletePrefix(ctx context.Context, prefix string) error {
	if prefix == "" {
		return ErrEmptyPrefix
	}

	pattern := globEscape(v.k(prefix)) + "*"
	cursor := "0"
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		rp, err := v.do(ctx, cmd("SCAN", cursor, "MATCH", pattern, "COUNT", strconv.Itoa(scanBatch))...)
		if err != nil {
			return err
		}
		if len(rp.arr) != 2 {
			return errProtocol
		}

		next := string(rp.arr[0].str)
		keys := rp.arr[1].arr
		for start := 0; start < len(keys); start += deleteBatch {
			end := start + deleteBatch
			if end > len(keys) {
				end = len(keys)
			}
			args := cmd("DEL")
			for _, item := range keys[start:end] {
				args = append(args, item.str)
			}
			if len(args) == 1 {
				continue
			}
			if _, err := v.do(ctx, args...); err != nil {
				return err
			}
		}

		if next == "" || next == "0" {
			return nil
		}
		cursor = next
	}
}

// Close drops every pooled connection. It is safe to call more than once.
func (v *valkeyCache) Close() error {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return nil
	}
	v.closed = true
	v.mu.Unlock()

	for {
		select {
		case conn := <-v.pool:
			conn.close()
		default:
			return nil
		}
	}
}

// do runs a command, retrying once on a transport failure so a
// server-side idle timeout on a pooled connection is invisible to
// callers. Server error replies are returned as-is and never retried.
func (v *valkeyCache) do(ctx context.Context, args ...[]byte) (reply, error) {
	rp, err := v.doOnce(ctx, args...)
	if err == nil {
		return rp, nil
	}

	var serverErr *ServerError
	if errors.As(err, &serverErr) || errors.Is(err, ErrClosed) || ctx.Err() != nil {
		return reply{}, err
	}

	rp, retryErr := v.doOnce(ctx, args...)
	if retryErr != nil {
		return reply{}, err
	}
	return rp, nil
}

// doOnce runs a command on exactly one connection, returning the
// connection to the pool only when it is still usable.
func (v *valkeyCache) doOnce(ctx context.Context, args ...[]byte) (reply, error) {
	conn, err := v.getConn(ctx)
	if err != nil {
		return reply{}, err
	}

	rp, err := conn.do(ctx, v.opts.Timeout, args...)
	if err == nil {
		v.putConn(conn)
		return rp, nil
	}

	var serverErr *ServerError
	if errors.As(err, &serverErr) {
		v.putConn(conn)
		return reply{}, err
	}

	conn.close()
	return reply{}, err
}

// getConn takes an idle connection from the pool or dials a new one.
func (v *valkeyCache) getConn(ctx context.Context) (*respConn, error) {
	v.mu.Lock()
	closed := v.closed
	v.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}

	select {
	case conn := <-v.pool:
		return conn, nil
	default:
	}

	return dialRESP(ctx, v.opts)
}

// putConn returns a connection to the pool, closing it when the pool is
// full or the cache has been closed.
func (v *valkeyCache) putConn(conn *respConn) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.closed {
		conn.close()
		return
	}
	select {
	case v.pool <- conn:
	default:
		conn.close()
	}
}

// k applies the configured key namespace.
func (v *valkeyCache) k(key string) string {
	return v.opts.KeyPrefix + key
}

// ttlMillis converts a TTL to whole milliseconds, rounding a sub
// millisecond TTL up so a positive TTL never becomes "no expiry".
func ttlMillis(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	ms := ttl.Milliseconds()
	if ms == 0 {
		return 1
	}
	return ms
}

// globEscape neutralizes the glob metacharacters SCAN MATCH understands,
// so a prefix containing one matches literally instead of expanding.
func globEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '*', '?', '[', ']', '^', '\\':
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
