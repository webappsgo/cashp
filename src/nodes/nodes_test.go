package nodes

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

// testClock is the injected clock every test drives by hand, so expiry,
// backoff and liveness are exercised without a single sleep.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

// now returns the current fake time.
func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// advance moves the fake clock forward.
func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeCluster records every cluster primitive it is asked for. Tests assert
// on calls to prove a managed node never reaches any of them.
type fakeCluster struct {
	mu      sync.Mutex
	calls   []string
	primary string
	removed []string
}

// record notes one cluster primitive invocation.
func (f *fakeCluster) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

// callCount returns how many times a primitive was invoked.
func (f *fakeCluster) callCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, call := range f.calls {
		if call == name {
			n++
		}
	}
	return n
}

// total returns how many cluster primitives were invoked in all.
func (f *fakeCluster) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// Heartbeat implements ClusterOps.
func (f *fakeCluster) Heartbeat(_ context.Context, _ database.Heartbeat) error {
	f.record("Heartbeat")
	return nil
}

// Nodes implements ClusterOps.
func (f *fakeCluster) Nodes(_ context.Context) ([]database.Node, error) {
	f.record("Nodes")
	return []database.Node{{ID: "cluster-a"}}, nil
}

// HealthyNodes implements ClusterOps.
func (f *fakeCluster) HealthyNodes(_ context.Context) ([]string, error) {
	f.record("HealthyNodes")
	return []string{"cluster-a"}, nil
}

// HasQuorum implements ClusterOps.
func (f *fakeCluster) HasQuorum(_ context.Context) (bool, error) {
	f.record("HasQuorum")
	return true, nil
}

// AcquireLock implements ClusterOps.
func (f *fakeCluster) AcquireLock(_ context.Context, name, owner string, _ time.Duration) (*database.Lock, error) {
	f.record("AcquireLock")
	return &database.Lock{Name: name, Owner: owner}, nil
}

// WithLock implements ClusterOps.
func (f *fakeCluster) WithLock(ctx context.Context, _, _ string, _ time.Duration, fn func(context.Context) error) error {
	f.record("WithLock")
	return fn(ctx)
}

// ElectPrimary implements ClusterOps.
func (f *fakeCluster) ElectPrimary(_ context.Context, nodeID string) (bool, error) {
	f.record("ElectPrimary")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.primary == "" || f.primary == nodeID {
		f.primary = nodeID
		return true, nil
	}
	return false, nil
}

// IsPrimary implements ClusterOps.
func (f *fakeCluster) IsPrimary(_ context.Context, nodeID string) (bool, error) {
	f.record("IsPrimary")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.primary == nodeID, nil
}

// PrimaryID implements ClusterOps.
func (f *fakeCluster) PrimaryID(_ context.Context) (string, error) {
	f.record("PrimaryID")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.primary, nil
}

// RemoveNode implements ClusterOps.
func (f *fakeCluster) RemoveNode(_ context.Context, nodeID string) error {
	f.record("RemoveNode")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, nodeID)
	return nil
}

// fakeDoer is the injected HTTP transport. It never opens a socket.
type fakeDoer struct {
	mu       sync.Mutex
	requests []*http.Request
	status   int
	err      error
}

// Do implements Doer.
func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       http.NoBody,
		Header:     make(http.Header),
	}, nil
}

// count returns how many requests the transport received.
func (f *fakeDoer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// last returns the most recent request.
func (f *fakeDoer) last() *http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return nil
	}
	return f.requests[len(f.requests)-1]
}

// testEnv is everything a test needs to drive the service.
type testEnv struct {
	svc     *Service
	cluster *fakeCluster
	clock   *testClock
	http    *fakeDoer
	ctx     context.Context
}

// newEnv builds a service over a temporary SQLite database with the clock
// and transport injected. No network connection, no root and no real node
// is involved anywhere in this package's tests.
func newEnv(t *testing.T) *testEnv {
	t.Helper()

	db, err := database.Open(database.Config{Driver: "sqlite", Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if err := db.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	clock := &testClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	cluster := &fakeCluster{}
	doer := &fakeDoer{}

	svc, err := New(Options{
		DB:      db,
		Cluster: cluster,
		Now:     clock.now,
		HTTP:    doer,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &testEnv{svc: svc, cluster: cluster, clock: clock, http: doer, ctx: context.Background()}
}

// goodFacts is a valid inventory a well-behaved node would report.
func goodFacts() Facts {
	return Facts{
		OS:          "linux",
		Arch:        "amd64",
		Kernel:      "6.6.0-cashp",
		Hostname:    "node-a.example",
		CPUCores:    8,
		MemoryBytes: 16 << 30,
		DiskBytes:   512 << 30,
		Backends:    []string{"docker", "apt"},
	}
}

// enroll issues a token and redeems it, returning the node and its
// credential.
func (e *testEnv) enroll(t *testing.T, role Role, nodeID string) (Node, string) {
	t.Helper()
	issued, err := e.svc.IssueEnrollmentToken(e.ctx, EnrollmentRequest{Role: role, Actor: "admin"})
	if err != nil {
		t.Fatalf("IssueEnrollmentToken: %v", err)
	}
	result, err := e.svc.Enroll(e.ctx, EnrollRequest{
		Secret:       issued.Secret,
		NodeID:       nodeID,
		Address:      "10.9.9.9:9443",
		AgentVersion: "1.0.0",
		Facts:        goodFacts(),
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	return result.Node, result.Credential
}

func TestRoleAndPrefixes(t *testing.T) {
	if !RoleCluster.Valid() || !RoleManaged.Valid() || Role("other").Valid() {
		t.Fatal("role validity is wrong")
	}
	if TokenPrefixFor(RoleCluster) != ClusterTokenPrefix {
		t.Fatalf("cluster prefix = %q", TokenPrefixFor(RoleCluster))
	}
	if TokenPrefixFor(RoleManaged) != ManagedTokenPrefix {
		t.Fatalf("managed prefix = %q", TokenPrefixFor(RoleManaged))
	}
}

func TestNewRequiresDatabase(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New without a database must fail")
	}
}

func TestEnrollmentTokenIsHashedAtRestAndMasked(t *testing.T) {
	e := newEnv(t)

	issued, err := e.svc.IssueEnrollmentToken(e.ctx, EnrollmentRequest{Role: RoleManaged, Actor: "admin"})
	if err != nil {
		t.Fatalf("IssueEnrollmentToken: %v", err)
	}
	if issued.Secret == "" {
		t.Fatal("secret must be returned once")
	}

	var stored int
	if err := e.svc.db.SQL().QueryRow(
		`SELECT COUNT(*) FROM node_enrollment_tokens WHERE token_hash = ?`, issued.Secret).Scan(&stored); err != nil {
		t.Fatalf("query: %v", err)
	}
	if stored != 0 {
		t.Fatal("plaintext enrollment token was stored")
	}

	listed, err := e.svc.ListEnrollmentTokens(e.ctx)
	if err != nil {
		t.Fatalf("ListEnrollmentTokens: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed %d tokens", len(listed))
	}
	display := listed[0].Display()
	if display == issued.Secret || len(display) >= len(issued.Secret) {
		t.Fatalf("token display %q is not masked", display)
	}
}

func TestEnrollmentTokenIsSingleUse(t *testing.T) {
	e := newEnv(t)

	issued, err := e.svc.IssueEnrollmentToken(e.ctx, EnrollmentRequest{Role: RoleManaged, Actor: "admin"})
	if err != nil {
		t.Fatalf("IssueEnrollmentToken: %v", err)
	}
	req := EnrollRequest{Secret: issued.Secret, NodeID: "node-a", Facts: goodFacts()}
	if _, err := e.svc.Enroll(e.ctx, req); err != nil {
		t.Fatalf("first Enroll: %v", err)
	}

	req.NodeID = "node-b"
	if _, err := e.svc.Enroll(e.ctx, req); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("second Enroll error = %v, want ErrEnrollmentRejected", err)
	}
}

func TestEnrollmentTokenBoundedUses(t *testing.T) {
	e := newEnv(t)

	issued, err := e.svc.IssueEnrollmentToken(e.ctx, EnrollmentRequest{
		Role: RoleManaged, MaxUses: 2, Actor: "admin",
	})
	if err != nil {
		t.Fatalf("IssueEnrollmentToken: %v", err)
	}
	for _, id := range []string{"node-a", "node-b"} {
		if _, err := e.svc.Enroll(e.ctx, EnrollRequest{Secret: issued.Secret, NodeID: id, Facts: goodFacts()}); err != nil {
			t.Fatalf("Enroll %s: %v", id, err)
		}
	}
	if _, err := e.svc.Enroll(e.ctx, EnrollRequest{Secret: issued.Secret, NodeID: "node-c", Facts: goodFacts()}); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("third Enroll error = %v, want ErrEnrollmentRejected", err)
	}
}

func TestEnrollmentTokenExpires(t *testing.T) {
	e := newEnv(t)

	issued, err := e.svc.IssueEnrollmentToken(e.ctx, EnrollmentRequest{
		Role: RoleManaged, TTL: time.Minute, Actor: "admin",
	})
	if err != nil {
		t.Fatalf("IssueEnrollmentToken: %v", err)
	}
	e.clock.advance(2 * time.Minute)

	if _, err := e.svc.Enroll(e.ctx, EnrollRequest{Secret: issued.Secret, NodeID: "node-a", Facts: goodFacts()}); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("expired Enroll error = %v, want ErrEnrollmentRejected", err)
	}

	closed, err := e.svc.ExpireEnrollmentTokens(e.ctx)
	if err != nil {
		t.Fatalf("ExpireEnrollmentTokens: %v", err)
	}
	if closed != 1 {
		t.Fatalf("expired %d tokens, want 1", closed)
	}
}

func TestEnrollmentTokenRevocation(t *testing.T) {
	e := newEnv(t)

	issued, err := e.svc.IssueEnrollmentToken(e.ctx, EnrollmentRequest{Role: RoleManaged, Actor: "admin"})
	if err != nil {
		t.Fatalf("IssueEnrollmentToken: %v", err)
	}
	if err := e.svc.RevokeEnrollmentToken(e.ctx, issued.Token.ID, "admin"); err != nil {
		t.Fatalf("RevokeEnrollmentToken: %v", err)
	}
	// Revoking twice is a no-op, so the call is safe to retry.
	if err := e.svc.RevokeEnrollmentToken(e.ctx, issued.Token.ID, "admin"); err != nil {
		t.Fatalf("second RevokeEnrollmentToken: %v", err)
	}
	if _, err := e.svc.Enroll(e.ctx, EnrollRequest{Secret: issued.Secret, NodeID: "node-a", Facts: goodFacts()}); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("revoked Enroll error = %v, want ErrEnrollmentRejected", err)
	}
}

func TestEnrollRejectsUnknownAndMalformedSecrets(t *testing.T) {
	e := newEnv(t)

	cases := []string{"", "not-a-token", "adm_deadbeef", ClusterTokenPrefix + "abcdefghijklmnopqrstuvwxyz012345"}
	for _, secret := range cases {
		if _, err := e.svc.Enroll(e.ctx, EnrollRequest{Secret: secret, NodeID: "node-a", Facts: goodFacts()}); !errors.Is(err, ErrEnrollmentRejected) {
			t.Fatalf("Enroll(%q) error = %v, want ErrEnrollmentRejected", secret, err)
		}
	}
}

func TestEnrollRejectsDuplicateNodeID(t *testing.T) {
	e := newEnv(t)
	e.enroll(t, RoleManaged, "node-a")

	issued, err := e.svc.IssueEnrollmentToken(e.ctx, EnrollmentRequest{Role: RoleManaged, Actor: "admin"})
	if err != nil {
		t.Fatalf("IssueEnrollmentToken: %v", err)
	}
	if _, err := e.svc.Enroll(e.ctx, EnrollRequest{Secret: issued.Secret, NodeID: "node-a", Facts: goodFacts()}); !errors.Is(err, ErrNodeExists) {
		t.Fatalf("duplicate Enroll error = %v, want ErrNodeExists", err)
	}
}

func TestAuthenticateAndCredentialLifecycle(t *testing.T) {
	e := newEnv(t)
	node, credential := e.enroll(t, RoleManaged, "node-a")

	id, err := e.svc.Authenticate(e.ctx, credential)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if id.Node.ID != node.ID || id.Node.Role != RoleManaged {
		t.Fatalf("identity = %+v", id.Node)
	}

	var plaintext int
	if err := e.svc.db.SQL().QueryRow(
		`SELECT COUNT(*) FROM node_credentials WHERE token_hash = ?`, credential).Scan(&plaintext); err != nil {
		t.Fatalf("query: %v", err)
	}
	if plaintext != 0 {
		t.Fatal("plaintext credential was stored")
	}

	if _, err := e.svc.RevokeCredentials(e.ctx, node.ID, "admin"); err != nil {
		t.Fatalf("RevokeCredentials: %v", err)
	}
	if _, err := e.svc.Authenticate(e.ctx, credential); !errors.Is(err, ErrCredentialRejected) {
		t.Fatalf("revoked Authenticate error = %v, want ErrCredentialRejected", err)
	}
}

func TestAuthenticateRejectsGarbage(t *testing.T) {
	e := newEnv(t)
	e.enroll(t, RoleManaged, "node-a")

	for _, secret := range []string{"", "nope", "usr_abcdefghijklmnopqrstuvwxyz012345"} {
		if _, err := e.svc.Authenticate(e.ctx, secret); !errors.Is(err, ErrCredentialRejected) {
			t.Fatalf("Authenticate(%q) error = %v, want ErrCredentialRejected", secret, err)
		}
	}
}

func TestCredentialExpiry(t *testing.T) {
	db, err := database.Open(database.Config{Driver: "sqlite", Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	clock := &testClock{t: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)}
	svc, err := New(Options{DB: db, Cluster: &fakeCluster{}, Now: clock.now, CredentialTTL: time.Hour})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	env := &testEnv{svc: svc, clock: clock, ctx: context.Background()}
	_, credential := env.enroll(t, RoleManaged, "node-a")

	if _, err := svc.Authenticate(env.ctx, credential); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	clock.advance(2 * time.Hour)
	if _, err := svc.Authenticate(env.ctx, credential); !errors.Is(err, ErrCredentialRejected) {
		t.Fatalf("expired Authenticate error = %v, want ErrCredentialRejected", err)
	}
}

func TestRekeyKeepsNodeAndRetiresOldCredential(t *testing.T) {
	e := newEnv(t)
	node, oldCredential := e.enroll(t, RoleManaged, "node-a")

	issued, err := e.svc.Rekey(e.ctx, node.ID, "admin")
	if err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	// The old credential still works until the re-key is completed, so an
	// abandoned re-key cannot lock a node out.
	if _, err := e.svc.Authenticate(e.ctx, oldCredential); err != nil {
		t.Fatalf("Authenticate before rejoin: %v", err)
	}

	result, err := e.svc.Enroll(e.ctx, EnrollRequest{
		Secret: issued.Secret,
		NodeID: "someone-else",
		Facts:  goodFacts(),
	})
	if err != nil {
		t.Fatalf("rejoin Enroll: %v", err)
	}
	// The bound node id wins over whatever the node asked for.
	if result.Node.ID != node.ID {
		t.Fatalf("rejoined node id = %q, want %q", result.Node.ID, node.ID)
	}
	if result.Node.State != StateEnrolled {
		t.Fatalf("rejoined state = %q", result.Node.State)
	}
	if result.Credential == oldCredential {
		t.Fatal("re-key must issue a different credential")
	}
	if _, err := e.svc.Authenticate(e.ctx, oldCredential); !errors.Is(err, ErrCredentialRejected) {
		t.Fatalf("old credential still valid after re-key: %v", err)
	}
	if _, err := e.svc.Authenticate(e.ctx, result.Credential); err != nil {
		t.Fatalf("Authenticate with new credential: %v", err)
	}
}

func TestRekeyRejectsRoleMismatch(t *testing.T) {
	e := newEnv(t)
	node, _ := e.enroll(t, RoleManaged, "node-a")

	if _, err := e.svc.IssueEnrollmentToken(e.ctx, EnrollmentRequest{
		Role: RoleCluster, NodeID: node.ID, Actor: "admin",
	}); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("cross-role bound token error = %v, want ErrInvalidRole", err)
	}
}

func TestFactsRoundTripAndHostileFactsRejected(t *testing.T) {
	e := newEnv(t)
	node, credential := e.enroll(t, RoleManaged, "node-a")

	facts, err := e.svc.Facts(e.ctx, node.ID)
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	if facts.OS != "linux" || facts.Arch != "amd64" || facts.CPUCores != 8 {
		t.Fatalf("facts = %+v", facts)
	}
	if len(facts.Backends) != 2 || facts.Backends[0] != "apt" || facts.Backends[1] != "docker" {
		t.Fatalf("backends = %v", facts.Backends)
	}

	id, err := e.svc.Authenticate(e.ctx, credential)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	hostile := goodFacts()
	hostile.Backends = []string{"docker; rm -rf /"}
	if err := e.svc.ReportFacts(e.ctx, id, hostile); !errors.Is(err, ErrInvalidFacts) {
		t.Fatalf("hostile ReportFacts error = %v, want ErrInvalidFacts", err)
	}

	updated := goodFacts()
	updated.CPUCores = 16
	if err := e.svc.ReportFacts(e.ctx, id, updated); err != nil {
		t.Fatalf("ReportFacts: %v", err)
	}
	facts, err = e.svc.Facts(e.ctx, node.ID)
	if err != nil {
		t.Fatalf("Facts after report: %v", err)
	}
	if facts.CPUCores != 16 {
		t.Fatalf("cpu cores = %d, want 16", facts.CPUCores)
	}
}

func TestListFiltersByRole(t *testing.T) {
	e := newEnv(t)
	e.enroll(t, RoleCluster, "cluster-a")
	e.enroll(t, RoleManaged, "managed-a")

	all, err := e.svc.List(e.ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("listed %d nodes, want 2", len(all))
	}
	managed, err := e.svc.List(e.ctx, RoleManaged)
	if err != nil {
		t.Fatalf("List(managed): %v", err)
	}
	if len(managed) != 1 || managed[0].ID != "managed-a" {
		t.Fatalf("managed list = %+v", managed)
	}
	if _, err := e.svc.List(e.ctx, Role("nope")); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("List(bad role) error = %v", err)
	}
	if _, err := e.svc.Get(e.ctx, "missing-node"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Get(missing) error = %v", err)
	}
}
