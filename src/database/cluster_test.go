package database

import (
	"context"
	"errors"
	"testing"
	"time"
)

// heartbeatFor builds a heartbeat payload for a node id.
func heartbeatFor(id string) Heartbeat {
	return Heartbeat{
		NodeID:     id,
		Address:    id + ".internal:8080",
		AppVersion: "1.0.0",
		CommitHash: "abc123",
		Secrets: SecretVersions{
			InstallationSecret:  2,
			ServerEncryptionKey: 1,
			CookieSigningKey:    3,
			CSRFTokenSecret:     4,
			LearnedOrigins:      5,
		},
		StartedAt: time.Now().UTC().Add(-time.Hour),
	}
}

// ageNode rewrites a node's last_seen so state transitions can be tested
// without waiting.
func ageNode(t *testing.T, db *DB, id string, age time.Duration) {
	t.Helper()
	when := time.Now().UTC().Add(-age).Unix()
	if _, err := db.ExecContext(context.Background(), TimeoutWrite,
		`UPDATE nodes SET last_seen = ? WHERE node_id = ?`, when, id); err != nil {
		t.Fatalf("age node %s: %v", id, err)
	}
}

func TestHeartbeatInsertsThenUpdates(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	hb := heartbeatFor("node-a")
	if err := db.Heartbeat(ctx, hb); err != nil {
		t.Fatalf("first heartbeat: %v", err)
	}
	if err := db.Heartbeat(ctx, hb); err != nil {
		t.Fatalf("second heartbeat: %v", err)
	}

	nodes, err := db.Nodes(ctx)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}

	node := nodes[0]
	if node.ID != "node-a" || node.Address != "node-a.internal:8080" {
		t.Errorf("node identity = %+v", node)
	}
	if node.AppVersion != "1.0.0" || node.CommitHash != "abc123" {
		t.Errorf("node build info = %+v", node)
	}
	if node.Secrets != hb.Secrets {
		t.Errorf("secret versions = %+v, want %+v", node.Secrets, hb.Secrets)
	}
	if node.Version != 2 {
		t.Errorf("row version = %d, want 2 after one update", node.Version)
	}
	if node.State != StateHealthy {
		t.Errorf("state = %q, want healthy", node.State)
	}
	if node.StartedAt.After(time.Now().UTC().Add(-time.Minute)) {
		t.Errorf("started_at should be preserved from the first heartbeat: %v", node.StartedAt)
	}
}

func TestHeartbeatRequiresNodeID(t *testing.T) {
	db := newTestDB(t)
	if err := db.Heartbeat(context.Background(), Heartbeat{}); err == nil {
		t.Fatal("expected an error for an empty node id")
	}
}

func TestGetNode(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := db.Heartbeat(ctx, heartbeatFor("node-a")); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	node, err := db.GetNode(ctx, "node-a")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if node.ID != "node-a" {
		t.Errorf("id = %q", node.ID)
	}

	if _, err := db.GetNode(ctx, "ghost"); !IsNotFound(err) {
		t.Errorf("GetNode(ghost) = %v, want not-found", err)
	}
}

func TestDeriveState(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		age   time.Duration
		stale bool
		want  NodeState
	}{
		{time.Second, false, StateHealthy},
		{DegradedAfter, false, StateDegraded},
		{OfflineAfter, false, StateOffline},
		{OfflineAfter + time.Hour, true, StateOffline},
		{time.Second, true, StateStale},
	}
	for _, tc := range cases {
		got := DeriveState(now.Add(-tc.age), tc.stale, now)
		if got != tc.want {
			t.Errorf("DeriveState(age=%v, stale=%v) = %q, want %q", tc.age, tc.stale, got, tc.want)
		}
	}
}

func TestMarkStaleExcludesFromHealthy(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	for _, id := range []string{"node-a", "node-b"} {
		if err := db.Heartbeat(ctx, heartbeatFor(id)); err != nil {
			t.Fatalf("heartbeat %s: %v", id, err)
		}
	}

	if err := db.MarkStale(ctx, "node-b", true); err != nil {
		t.Fatalf("MarkStale: %v", err)
	}

	healthy, err := db.HealthyNodes(ctx)
	if err != nil {
		t.Fatalf("HealthyNodes: %v", err)
	}
	if len(healthy) != 1 || healthy[0] != "node-a" {
		t.Fatalf("healthy = %v, want [node-a]", healthy)
	}

	node, err := db.GetNode(ctx, "node-b")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if !node.Stale || node.State != StateStale {
		t.Errorf("node-b = %+v, want stale", node)
	}

	if err := db.MarkStale(ctx, "node-b", false); err != nil {
		t.Fatalf("MarkStale clear: %v", err)
	}
	healthy, err = db.HealthyNodes(ctx)
	if err != nil {
		t.Fatalf("HealthyNodes: %v", err)
	}
	if len(healthy) != 2 {
		t.Errorf("healthy = %v, want both nodes", healthy)
	}
}

func TestHasQuorum(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// A fresh instance with no registered nodes trivially has quorum.
	ok, err := db.HasQuorum(ctx)
	if err != nil {
		t.Fatalf("HasQuorum: %v", err)
	}
	if !ok {
		t.Error("empty cluster must have quorum")
	}

	for _, id := range []string{"node-a", "node-b", "node-c"} {
		if err := db.Heartbeat(ctx, heartbeatFor(id)); err != nil {
			t.Fatalf("heartbeat %s: %v", id, err)
		}
	}
	if ok, err = db.HasQuorum(ctx); err != nil || !ok {
		t.Fatalf("HasQuorum with three healthy nodes = %v (%v)", ok, err)
	}

	ageNode(t, db, "node-b", 10*time.Minute)
	if ok, err = db.HasQuorum(ctx); err != nil || !ok {
		t.Fatalf("HasQuorum with two of three healthy = %v (%v)", ok, err)
	}

	ageNode(t, db, "node-c", 10*time.Minute)
	if ok, err = db.HasQuorum(ctx); err != nil {
		t.Fatalf("HasQuorum: %v", err)
	} else if ok {
		t.Error("one of three healthy nodes must not be a quorum")
	}
}

func TestLockLifecycle(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	lock, err := db.AcquireLock(ctx, "geoip.update", "node-a", time.Minute)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if lock.Owner != "node-a" || lock.Token == "" {
		t.Errorf("lock = %+v", lock)
	}

	if _, err := db.AcquireLock(ctx, "geoip.update", "node-b", time.Minute); !errors.Is(err, ErrLockHeld) {
		t.Errorf("second acquire = %v, want ErrLockHeld", err)
	}

	if err := lock.Refresh(ctx, 2*time.Minute); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if err := lock.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := lock.Refresh(ctx, time.Minute); !errors.Is(err, ErrLockHeld) {
		t.Errorf("Refresh after release = %v, want ErrLockHeld", err)
	}

	// Once released, another node may take it.
	second, err := db.AcquireLock(ctx, "geoip.update", "node-b", time.Minute)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	if err := second.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestExpiredLockIsReclaimed(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.AcquireLock(ctx, "cron.nightly", "node-a", time.Millisecond); err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)

	lock, err := db.AcquireLock(ctx, "cron.nightly", "node-b", time.Minute)
	if err != nil {
		t.Fatalf("reclaim expired lock: %v", err)
	}
	if lock.Owner != "node-b" {
		t.Errorf("owner = %q, want node-b", lock.Owner)
	}
}

func TestAcquireLockRequiresName(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.AcquireLock(context.Background(), "  ", "node-a", time.Minute); err == nil {
		t.Fatal("expected an error for an empty lock name")
	}
}

func TestWithLock(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	ran := false
	err := db.WithLock(ctx, "task.sync", "node-a", time.Minute, func(context.Context) error {
		ran = true
		// While held, no other node may run the same task.
		if _, err := db.AcquireLock(ctx, "task.sync", "node-b", time.Minute); !errors.Is(err, ErrLockHeld) {
			t.Errorf("inner acquire = %v, want ErrLockHeld", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithLock: %v", err)
	}
	if !ran {
		t.Error("WithLock did not run the function")
	}

	// The lock must be released afterwards.
	lock, err := db.AcquireLock(ctx, "task.sync", "node-b", time.Minute)
	if err != nil {
		t.Fatalf("acquire after WithLock: %v", err)
	}
	if err := lock.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}

	sentinel := errors.New("task failed")
	if err := db.WithLock(ctx, "task.sync", "node-a", time.Minute, func(context.Context) error {
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Errorf("WithLock error = %v, want sentinel", err)
	}
}

func TestElectPrimary(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	for _, id := range []string{"node-a", "node-b"} {
		if err := db.Heartbeat(ctx, heartbeatFor(id)); err != nil {
			t.Fatalf("heartbeat %s: %v", id, err)
		}
	}

	// The higher id must not claim the lease while the lowest node is healthy.
	primary, err := db.ElectPrimary(ctx, "node-b")
	if err != nil {
		t.Fatalf("ElectPrimary(node-b): %v", err)
	}
	if primary {
		t.Error("node-b must not become primary while node-a is healthy")
	}

	primary, err = db.ElectPrimary(ctx, "node-a")
	if err != nil {
		t.Fatalf("ElectPrimary(node-a): %v", err)
	}
	if !primary {
		t.Fatal("node-a must become primary")
	}

	owner, err := db.PrimaryID(ctx)
	if err != nil {
		t.Fatalf("PrimaryID: %v", err)
	}
	if owner != "node-a" {
		t.Errorf("PrimaryID = %q, want node-a", owner)
	}
	if isPrimary, err := db.IsPrimary(ctx, "node-a"); err != nil || !isPrimary {
		t.Errorf("IsPrimary(node-a) = %v (%v)", isPrimary, err)
	}
	if isPrimary, err := db.IsPrimary(ctx, "node-b"); err != nil || isPrimary {
		t.Errorf("IsPrimary(node-b) = %v (%v)", isPrimary, err)
	}

	// The incumbent renews; the other node stays secondary (no preemption).
	if primary, err = db.ElectPrimary(ctx, "node-a"); err != nil || !primary {
		t.Errorf("incumbent renewal = %v (%v)", primary, err)
	}
	if primary, err = db.ElectPrimary(ctx, "node-b"); err != nil || primary {
		t.Errorf("node-b preempted a live lease: %v (%v)", primary, err)
	}

	// Removing the primary frees the lease for the next healthy node.
	if err := db.RemoveNode(ctx, "node-a"); err != nil {
		t.Fatalf("RemoveNode: %v", err)
	}
	if owner, err = db.PrimaryID(ctx); err != nil || owner != "" {
		t.Errorf("PrimaryID after removal = %q (%v)", owner, err)
	}
	if primary, err = db.ElectPrimary(ctx, "node-b"); err != nil || !primary {
		t.Errorf("node-b should take over: %v (%v)", primary, err)
	}

	nodes, err := db.Nodes(ctx)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != "node-b" {
		t.Errorf("nodes after removal = %+v", nodes)
	}
}

func TestElectPrimaryOnUnregisteredSingleNode(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	primary, err := db.ElectPrimary(ctx, "solo")
	if err != nil {
		t.Fatalf("ElectPrimary: %v", err)
	}
	if !primary {
		t.Error("a lone node with no registrations must become primary")
	}
	if _, err := db.ElectPrimary(ctx, ""); err == nil {
		t.Error("expected an error for an empty node id")
	}
}

func TestClusterSchemaCoversAllDrivers(t *testing.T) {
	for _, driver := range []string{DriverSQLite, DriverPostgres, DriverMySQL, DriverSQLServer, DriverLibSQL} {
		stmts := clusterSchema(driver)
		if len(stmts) != 4 {
			t.Fatalf("%s: got %d statements, want 4", driver, len(stmts))
		}
		for _, stmt := range stmts {
			if stmt == "" {
				t.Errorf("%s: empty statement", driver)
			}
		}
	}
}
