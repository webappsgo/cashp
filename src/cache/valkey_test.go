package cache

import (
	"bufio"
	"context"
	"errors"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeEntry is one value held by the fake server.
type fakeEntry struct {
	val       []byte
	expiresAt time.Time
}

// fakeServer is a minimal in-process Valkey/Redis server implementing the
// exact command subset this package uses, so the RESP client is tested
// end to end without any external service.
type fakeServer struct {
	ln       net.Listener
	mu       sync.Mutex
	data     map[string]fakeEntry
	scans    map[string][]string
	conns    []net.Conn
	cursorID int
	password string
	done     chan struct{}
}

// newFakeServer starts a fake server on a loopback port and stops it when
// the test finishes.
func newFakeServer(t *testing.T, password string) *fakeServer {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	s := &fakeServer{
		ln:       ln,
		data:     make(map[string]fakeEntry),
		scans:    make(map[string][]string),
		password: password,
		done:     make(chan struct{}),
	}
	go s.accept()
	t.Cleanup(s.stop)
	return s
}

// addr is the "host:port" the client should dial.
func (s *fakeServer) addr() string {
	return s.ln.Addr().String()
}

// stop closes the listener and every accepted connection.
func (s *fakeServer) stop() {
	select {
	case <-s.done:
		return
	default:
	}
	close(s.done)
	_ = s.ln.Close()
	s.closeConns()
}

// closeConns drops every accepted connection, simulating a server-side
// idle timeout.
func (s *fakeServer) closeConns() {
	s.mu.Lock()
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()

	for _, c := range conns {
		_ = c.Close()
	}
}

// keys returns every stored key.
func (s *fakeServer) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.data))
	for k := range s.data {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// accept serves connections until the listener is closed.
func (s *fakeServer) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, conn)
		s.mu.Unlock()
		go s.handle(conn)
	}
}

// handle reads and answers commands on one connection.
func (s *fakeServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	authed := s.password == ""

	for {
		rp, err := readReply(r, 0)
		if err != nil {
			return
		}

		args := make([][]byte, 0, len(rp.arr))
		for _, item := range rp.arr {
			args = append(args, item.str)
		}
		if len(args) == 0 {
			return
		}

		if !s.dispatch(w, args, &authed) {
			return
		}
		if err := w.Flush(); err != nil {
			return
		}
	}
}

// dispatch answers a single command and reports whether the connection
// should stay open.
func (s *fakeServer) dispatch(w *bufio.Writer, args [][]byte, authed *bool) bool {
	command := strings.ToUpper(string(args[0]))

	if command == "AUTH" {
		want := s.password
		if len(args) >= 2 && string(args[len(args)-1]) == want {
			*authed = true
			writeStatus(w, "OK")
		} else {
			writeError(w, "WRONGPASS invalid username-password pair")
		}
		return true
	}
	if !*authed {
		writeError(w, "NOAUTH Authentication required")
		return true
	}

	switch command {
	case "PING":
		writeStatus(w, "PONG")
	case "SELECT":
		writeStatus(w, "OK")
	case "GET":
		s.get(w, string(args[1]))
	case "SET":
		s.set(w, args)
	case "DEL":
		s.del(w, args[1:])
	case "SCAN":
		s.scan(w, args)
	default:
		writeError(w, "ERR unknown command '"+command+"'")
	}
	return true
}

// get answers GET.
func (s *fakeServer) get(w *bufio.Writer, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.data[key]
	if !ok || expiredAt(e, time.Now()) {
		delete(s.data, key)
		writeNull(w)
		return
	}
	writeBulk(w, e.val)
}

// set answers SET, honoring the PX and NX modifiers.
func (s *fakeServer) set(w *bufio.Writer, args [][]byte) {
	key := string(args[1])
	val := args[2]

	var ttl time.Duration
	nx := false
	for i := 3; i < len(args); i++ {
		switch strings.ToUpper(string(args[i])) {
		case "PX":
			if i+1 >= len(args) {
				writeError(w, "ERR syntax error")
				return
			}
			ms, err := strconv.ParseInt(string(args[i+1]), 10, 64)
			if err != nil {
				writeError(w, "ERR value is not an integer or out of range")
				return
			}
			ttl = time.Duration(ms) * time.Millisecond
			i++
		case "NX":
			nx = true
		default:
			writeError(w, "ERR syntax error")
			return
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if nx {
		if e, ok := s.data[key]; ok && !expiredAt(e, time.Now()) {
			writeNull(w)
			return
		}
	}

	entry := fakeEntry{val: append([]byte(nil), val...)}
	if ttl > 0 {
		entry.expiresAt = time.Now().Add(ttl)
	}
	s.data[key] = entry
	writeStatus(w, "OK")
}

// del answers DEL.
func (s *fakeServer) del(w *bufio.Writer, keys [][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for _, k := range keys {
		if _, ok := s.data[string(k)]; ok {
			delete(s.data, string(k))
			removed++
		}
	}
	writeInt(w, int64(removed))
}

// scan answers SCAN, deliberately returning one key per round so the
// client's cursor loop is exercised.
func (s *fakeServer) scan(w *bufio.Writer, args [][]byte) {
	cursor := string(args[1])
	pattern := "*"
	for i := 2; i+1 < len(args); i += 2 {
		if strings.ToUpper(string(args[i])) == "MATCH" {
			pattern = string(args[i+1])
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if cursor != "0" {
		rest := s.scans[cursor]
		delete(s.scans, cursor)
		writeScan(w, "0", rest)
		return
	}

	now := time.Now()
	matched := make([]string, 0, len(s.data))
	for k, e := range s.data {
		if expiredAt(e, now) {
			continue
		}
		ok, err := path.Match(pattern, k)
		if err == nil && ok {
			matched = append(matched, k)
		}
	}
	sort.Strings(matched)

	if len(matched) <= 1 {
		writeScan(w, "0", matched)
		return
	}

	s.cursorID++
	next := strconv.Itoa(s.cursorID)
	s.scans[next] = matched[1:]
	writeScan(w, next, matched[:1])
}

// expiredAt reports whether an entry has expired.
func expiredAt(e fakeEntry, now time.Time) bool {
	return !e.expiresAt.IsZero() && now.After(e.expiresAt)
}

func writeStatus(w *bufio.Writer, s string) {
	_, _ = w.WriteString("+" + s + "\r\n")
}

func writeError(w *bufio.Writer, s string) {
	_, _ = w.WriteString("-" + s + "\r\n")
}

func writeInt(w *bufio.Writer, n int64) {
	_, _ = w.WriteString(":" + strconv.FormatInt(n, 10) + "\r\n")
}

func writeNull(w *bufio.Writer) {
	_, _ = w.WriteString("$-1\r\n")
}

func writeBulk(w *bufio.Writer, val []byte) {
	_, _ = w.WriteString("$" + strconv.Itoa(len(val)) + "\r\n")
	_, _ = w.Write(val)
	_, _ = w.WriteString("\r\n")
}

func writeScan(w *bufio.Writer, cursor string, keys []string) {
	_, _ = w.WriteString("*2\r\n")
	writeBulk(w, []byte(cursor))
	_, _ = w.WriteString("*" + strconv.Itoa(len(keys)) + "\r\n")
	for _, k := range keys {
		writeBulk(w, []byte(k))
	}
}

func newTestValkey(t *testing.T, opts Options) Cache {
	t.Helper()

	c, err := NewValkey(opts)
	if err != nil {
		t.Fatalf("NewValkey: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestValkeySetGetDelete(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer(t, "")
	c := newTestValkey(t, Options{Addr: s.addr()})

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
}

func TestValkeyBinaryValues(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer(t, "")
	c := newTestValkey(t, Options{Addr: s.addr()})

	payload := []byte{0x00, '\r', '\n', 0xfe, 0xff}
	if err := c.Set(ctx, "blob", payload, 0); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok, err := c.Get(ctx, "blob")
	if err != nil || !ok || string(got) != string(payload) {
		t.Fatalf("get = %v, %v, %v", got, ok, err)
	}
}

func TestValkeyTTL(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer(t, "")
	c := newTestValkey(t, Options{Addr: s.addr()})

	if err := c.Set(ctx, "page:home", []byte("x"), 20*time.Millisecond); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, ok, _ := c.Get(ctx, "page:home"); !ok {
		t.Fatal("value should be present before expiry")
	}

	time.Sleep(40 * time.Millisecond)
	if _, ok, _ := c.Get(ctx, "page:home"); ok {
		t.Fatal("expired value must not be returned")
	}
}

func TestValkeySetNX(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer(t, "")
	c := newTestValkey(t, Options{Addr: s.addr()})

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

	if err := ReleaseLock(ctx, c, "backup", "node-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, held, _ := c.Get(ctx, LockKey("backup")); held {
		t.Fatal("owner release must remove the lock")
	}
}

func TestValkeyDeletePrefixWalksEveryCursorPage(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer(t, "")
	c := newTestValkey(t, Options{Addr: s.addr()})

	for _, k := range []string{"user:1", "user:1:profile", "user:1:sessions", "user:12", "org:1"} {
		if err := c.Set(ctx, k, []byte("x"), 0); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}

	if err := InvalidateUser(ctx, c, 1); err != nil {
		t.Fatalf("InvalidateUser: %v", err)
	}

	got := s.keys()
	want := []string{"org:1", "user:12"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("remaining keys = %v, want %v", got, want)
	}
}

func TestValkeyDeletePrefixEscapesGlobCharacters(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer(t, "")
	c := newTestValkey(t, Options{Addr: s.addr()})

	for _, k := range []string{"lit*:a", "litx:a"} {
		if err := c.Set(ctx, k, []byte("x"), 0); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}

	if err := c.DeletePrefix(ctx, "lit*"); err != nil {
		t.Fatalf("delete prefix: %v", err)
	}
	if got := s.keys(); strings.Join(got, ",") != "litx:a" {
		t.Fatalf("remaining keys = %v, want [litx:a]", got)
	}
}

func TestValkeyKeyPrefixNamespacesKeys(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer(t, "")
	c := newTestValkey(t, Options{Addr: s.addr(), KeyPrefix: "cashp:"})

	if err := c.Set(ctx, "user:1", []byte("alice"), 0); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := s.keys(); strings.Join(got, ",") != "cashp:user:1" {
		t.Fatalf("stored keys = %v", got)
	}
	if _, ok, _ := c.Get(ctx, "user:1"); !ok {
		t.Fatal("namespaced key not readable through the cache")
	}
	if err := InvalidateUser(ctx, c, 1); err != nil {
		t.Fatalf("InvalidateUser: %v", err)
	}
	if got := s.keys(); len(got) != 0 {
		t.Fatalf("remaining keys = %v", got)
	}
}

func TestValkeyReconnectsAfterServerClosesConnection(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer(t, "")
	c := newTestValkey(t, Options{Addr: s.addr()})

	if err := c.Set(ctx, "user:1", []byte("alice"), 0); err != nil {
		t.Fatalf("set: %v", err)
	}
	s.closeConns()

	val, ok, err := c.Get(ctx, "user:1")
	if err != nil {
		t.Fatalf("get after reconnect: %v", err)
	}
	if !ok || string(val) != "alice" {
		t.Fatalf("get = %q, ok = %v", val, ok)
	}
}

func TestValkeyServerErrorIsReported(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer(t, "")
	c := newTestValkey(t, Options{Addr: s.addr()}).(*valkeyCache)

	_, err := c.do(ctx, cmd("BOGUS")...)
	var serverErr *ServerError
	if !errors.As(err, &serverErr) {
		t.Fatalf("err = %v, want *ServerError", err)
	}
	if !strings.Contains(serverErr.Msg, "unknown command") {
		t.Fatalf("msg = %q", serverErr.Msg)
	}
}

func TestValkeyAuthentication(t *testing.T) {
	s := newFakeServer(t, "s3cret")

	c := newTestValkey(t, Options{Addr: s.addr(), Password: "s3cret", Username: "default", DB: 2})
	if err := c.Set(context.Background(), "user:1", []byte("alice"), 0); err != nil {
		t.Fatalf("set: %v", err)
	}

	_, err := NewValkey(Options{Addr: s.addr(), Password: "wrong"})
	if !errors.Is(err, errAuthFailed) {
		t.Fatalf("err = %v, want errAuthFailed", err)
	}
	if err != nil && strings.Contains(err.Error(), "wrong") {
		t.Fatalf("auth error leaked the credential: %v", err)
	}
}

func TestValkeyDialFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := NewValkey(Options{Addr: addr, DialTimeout: 200 * time.Millisecond}); err == nil {
		t.Fatal("connecting to a closed port must fail")
	}
}

func TestValkeyClosed(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer(t, "")
	c, err := NewValkey(Options{Addr: s.addr()})
	if err != nil {
		t.Fatalf("NewValkey: %v", err)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close must be idempotent: %v", err)
	}

	if _, _, err := c.Get(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("Get: %v", err)
	}
	if err := c.Set(ctx, "k", []byte("v"), 0); !errors.Is(err, ErrClosed) {
		t.Errorf("Set: %v", err)
	}
	if err := c.Delete(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("Delete: %v", err)
	}
	if err := c.DeletePrefix(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("DeletePrefix: %v", err)
	}
}

func TestValkeyEmptyKeyRejected(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer(t, "")
	c := newTestValkey(t, Options{Addr: s.addr()}).(*valkeyCache)

	if _, _, err := c.Get(ctx, ""); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("Get: %v", err)
	}
	if err := c.Set(ctx, "", nil, 0); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("Set: %v", err)
	}
	if err := c.Delete(ctx, ""); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("Delete: %v", err)
	}
	if err := c.DeletePrefix(ctx, ""); !errors.Is(err, ErrEmptyPrefix) {
		t.Errorf("DeletePrefix: %v", err)
	}
	if _, err := c.SetNX(ctx, "", nil, time.Minute); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("SetNX: %v", err)
	}
}

func TestValkeyConcurrentCommands(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer(t, "")
	c := newTestValkey(t, Options{Addr: s.addr(), PoolSize: 3})

	var wg sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				key := "user:" + strconv.Itoa(worker) + ":" + strconv.Itoa(i)
				if err := c.Set(ctx, key, []byte("x"), time.Minute); err != nil {
					t.Errorf("set: %v", err)
					return
				}
				if _, ok, err := c.Get(ctx, key); err != nil || !ok {
					t.Errorf("get: ok = %v, err = %v", ok, err)
					return
				}
				if err := c.Delete(ctx, key); err != nil {
					t.Errorf("delete: %v", err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
}

func TestNewValkeyThroughDriverSelection(t *testing.T) {
	s := newFakeServer(t, "")

	for _, driver := range []string{DriverValkey, DriverRedis} {
		c, err := New(Options{Driver: driver, Addr: s.addr()})
		if err != nil {
			t.Fatalf("%s: %v", driver, err)
		}
		if _, ok := c.(*valkeyCache); !ok {
			t.Fatalf("%s produced %T", driver, c)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}

	c, err := NewRedis(Options{Addr: s.addr()})
	if err != nil {
		t.Fatalf("NewRedis: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestWithCacheDefaults(t *testing.T) {
	opts := withCacheDefaults(Options{})
	if opts.Addr != defaultAddr || opts.PoolSize != defaultPoolSize {
		t.Fatalf("opts = %+v", opts)
	}
	if opts.DialTimeout != defaultDialTimeout || opts.Timeout != defaultTimeout {
		t.Fatalf("timeouts = %v/%v", opts.DialTimeout, opts.Timeout)
	}

	custom := withCacheDefaults(Options{Addr: "10.0.0.1:6380", PoolSize: 2, DialTimeout: time.Second, Timeout: 2 * time.Second})
	if custom.Addr != "10.0.0.1:6380" || custom.PoolSize != 2 || custom.Timeout != 2*time.Second {
		t.Fatalf("custom options overwritten: %+v", custom)
	}
}
