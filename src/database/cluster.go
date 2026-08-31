package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Cluster support per AI.md PART 10 -> "Cluster Support". Every deployment
// gets these primitives; a single instance is simply a cluster of one, where
// quorum is trivially satisfied and the single node always wins the primary
// lease. Config sync and session sharing are built on top of the shared
// database by the packages that own those tables; this file owns node
// registration, heartbeats, distributed locks and primary election.
const (
	// HeartbeatInterval is how often each node writes its heartbeat.
	HeartbeatInterval = 30 * time.Second
	// DegradedAfter marks a node degraded once three heartbeats are missed.
	DegradedAfter = 90 * time.Second
	// OfflineAfter excludes a node from cluster operations.
	OfflineAfter = 5 * time.Minute
	// PrimaryLeaseTTL is how long a primary lease survives without renewal.
	PrimaryLeaseTTL = 90 * time.Second
	// SecretDriftGrace is the installation_secret rotation grace window;
	// a node lagging longer than this is marked stale.
	SecretDriftGrace = 7 * 24 * time.Hour
	// PrimaryLockName is the cluster_locks row backing primary election.
	PrimaryLockName = "cluster.primary"
)

// NodeState is the health state of a cluster node.
type NodeState string

// Node states per AI.md PART 10 -> "Node States".
const (
	// StateHealthy means a heartbeat arrived within DegradedAfter.
	StateHealthy NodeState = "healthy"
	// StateDegraded means heartbeats have been missing for DegradedAfter.
	StateDegraded NodeState = "degraded"
	// StateOffline means heartbeats have been missing for OfflineAfter.
	StateOffline NodeState = "offline"
	// StateStale means the node's secret versions have drifted beyond the
	// rotation grace window; it is excluded until it catches up.
	StateStale NodeState = "stale"
)

// ErrLockHeld is returned when a distributed lock is owned by another node.
var ErrLockHeld = errors.New("cluster: lock is held by another node")

// SecretVersions carries the per-secret version numbers a node reports in
// its heartbeat so the primary can detect rotation drift.
type SecretVersions struct {
	// InstallationSecret is the app_secrets version this node has loaded.
	InstallationSecret int64
	// ServerEncryptionKey is the node's server.yml encryption key version.
	ServerEncryptionKey int64
	// CookieSigningKey is the app_secrets version this node has loaded.
	CookieSigningKey int64
	// CSRFTokenSecret is the app_secrets version this node has loaded.
	CSRFTokenSecret int64
	// LearnedOrigins is the newest learned_origins timestamp this node read.
	LearnedOrigins int64
}

// Heartbeat is the payload a node writes every HeartbeatInterval.
type Heartbeat struct {
	// NodeID is the hostname or operator-assigned identifier.
	NodeID string
	// Address is the reachable host:port for this node.
	Address string
	// AppVersion is the running binary's version.
	AppVersion string
	// CommitHash is the build commit of the running binary.
	CommitHash string
	// Secrets holds the node's current secret version numbers.
	Secrets SecretVersions
	// StartedAt is when this node's process started; recorded on first
	// registration only.
	StartedAt time.Time
}

// Node is a cluster member as stored in the nodes table.
type Node struct {
	// ID is the node identifier.
	ID string
	// Address is the reachable host:port for this node.
	Address string
	// AppVersion is the version the node last reported.
	AppVersion string
	// CommitHash is the build commit the node last reported.
	CommitHash string
	// Secrets holds the last reported secret version numbers.
	Secrets SecretVersions
	// StartedAt is when the node process started.
	StartedAt time.Time
	// LastSeen is the timestamp of the node's last heartbeat.
	LastSeen time.Time
	// Stale is true when an operator or the primary flagged secret drift.
	Stale bool
	// Version is the optimistic-locking row version.
	Version int64
	// State is derived from LastSeen and Stale at read time.
	State NodeState
}

// Lock is an acquired distributed lock. Release must be called when the
// protected work finishes.
type Lock struct {
	db *DB
	// Name is the lock identifier.
	Name string
	// Owner is the node that holds the lock.
	Owner string
	// Token uniquely identifies this acquisition, so a stale holder cannot
	// release a lock that has since been taken over.
	Token string
	// ExpiresAt is when the lock lapses if it is not refreshed.
	ExpiresAt time.Time
}

// The cluster tables are registered first so every other package's schema
// can rely on them existing.
func init() {
	RegisterSchema("cluster", clusterSchema)
}

// clusterSchema returns the idempotent DDL for the cluster tables.
func clusterSchema(driver string) []string {
	d := DialectFor(driver)
	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS nodes (
	node_id %[1]s NOT NULL PRIMARY KEY,
	address %[2]s NOT NULL DEFAULT '',
	app_version %[2]s NOT NULL DEFAULT '',
	commit_hash %[2]s NOT NULL DEFAULT '',
	installation_secret_version %[3]s NOT NULL DEFAULT 0,
	server_security_encryption_key_version %[3]s NOT NULL DEFAULT 0,
	cookie_signing_key_version %[3]s NOT NULL DEFAULT 0,
	csrf_token_secret_version %[3]s NOT NULL DEFAULT 0,
	learned_origins_version %[3]s NOT NULL DEFAULT 0,
	started_at %[3]s NOT NULL DEFAULT 0,
	last_seen %[3]s NOT NULL DEFAULT 0,
	stale %[3]s NOT NULL DEFAULT 0,
	version %[3]s NOT NULL DEFAULT 1
)`, d.Key, d.Text, d.Int),
		CreateIndex(driver, "idx_nodes_last_seen", "nodes", "last_seen"),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS cluster_locks (
	lock_name %[1]s NOT NULL PRIMARY KEY,
	owner %[2]s NOT NULL DEFAULT '',
	token %[2]s NOT NULL DEFAULT '',
	acquired_at %[3]s NOT NULL DEFAULT 0,
	expires_at %[3]s NOT NULL DEFAULT 0
)`, d.Key, d.Text, d.Int),
		CreateIndex(driver, "idx_cluster_locks_expires_at", "cluster_locks", "expires_at"),
	}
}

const nodeColumns = `node_id, address, app_version, commit_hash,
	installation_secret_version, server_security_encryption_key_version,
	cookie_signing_key_version, csrf_token_secret_version,
	learned_origins_version, started_at, last_seen, stale, version`

// Heartbeat registers or refreshes this node's row in the nodes table. It is
// an upsert expressed as UPDATE-then-INSERT so it works on every supported
// driver without dialect-specific conflict syntax.
func (db *DB) Heartbeat(ctx context.Context, hb Heartbeat) error {
	if strings.TrimSpace(hb.NodeID) == "" {
		return errors.New("cluster: heartbeat requires a node id")
	}
	now := time.Now().UTC()
	started := hb.StartedAt
	if started.IsZero() {
		started = now
	}

	return db.Tx(ctx, TimeoutWrite, func(tx *sql.Tx) error {
		updated, err := db.heartbeatUpdate(ctx, tx, hb, now)
		if err != nil {
			return err
		}
		if updated {
			return nil
		}

		_, err = db.TxExec(ctx, tx, `INSERT INTO nodes (`+nodeColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 1)`,
			hb.NodeID, hb.Address, hb.AppVersion, hb.CommitHash,
			hb.Secrets.InstallationSecret, hb.Secrets.ServerEncryptionKey,
			hb.Secrets.CookieSigningKey, hb.Secrets.CSRFTokenSecret,
			hb.Secrets.LearnedOrigins, started.Unix(), now.Unix())
		if err == nil {
			return nil
		}
		if !isDuplicateRow(err) {
			return err
		}
		// Another node inserted the same row between the update and the
		// insert; fold this heartbeat into the existing row.
		if _, err := db.heartbeatUpdate(ctx, tx, hb, now); err != nil {
			return err
		}
		return nil
	})
}

// heartbeatUpdate refreshes an existing node row and reports whether one was
// matched.
func (db *DB) heartbeatUpdate(ctx context.Context, tx *sql.Tx, hb Heartbeat, now time.Time) (bool, error) {
	res, err := db.TxExec(ctx, tx, `UPDATE nodes SET
			address = ?,
			app_version = ?,
			commit_hash = ?,
			installation_secret_version = ?,
			server_security_encryption_key_version = ?,
			cookie_signing_key_version = ?,
			csrf_token_secret_version = ?,
			learned_origins_version = ?,
			last_seen = ?,
			version = version + 1
		WHERE node_id = ?`,
		hb.Address, hb.AppVersion, hb.CommitHash,
		hb.Secrets.InstallationSecret, hb.Secrets.ServerEncryptionKey,
		hb.Secrets.CookieSigningKey, hb.Secrets.CSRFTokenSecret,
		hb.Secrets.LearnedOrigins, now.Unix(), hb.NodeID)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, Classify(err)
	}
	return rows > 0, nil
}

// Nodes returns every registered cluster node with its derived state,
// ordered by node id.
func (db *DB) Nodes(ctx context.Context) ([]Node, error) {
	rows, err := db.QueryContext(ctx, TimeoutSelect,
		`SELECT `+nodeColumns+` FROM nodes ORDER BY node_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	now := time.Now().UTC()
	var out []Node
	for rows.Next() {
		node, err := scanNode(rows, now)
		if err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	if err := rows.Err(); err != nil {
		return nil, Classify(err)
	}
	return out, nil
}

// GetNode returns a single node by id.
func (db *DB) GetNode(ctx context.Context, nodeID string) (Node, error) {
	row := db.QueryRowContext(ctx, TimeoutSelect,
		`SELECT `+nodeColumns+` FROM nodes WHERE node_id = ?`, nodeID)
	return scanNode(row, time.Now().UTC())
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanNode reads one node row and derives its state.
func scanNode(sc rowScanner, now time.Time) (Node, error) {
	var (
		node      Node
		startedAt int64
		lastSeen  int64
		stale     int64
	)
	err := sc.Scan(&node.ID, &node.Address, &node.AppVersion, &node.CommitHash,
		&node.Secrets.InstallationSecret, &node.Secrets.ServerEncryptionKey,
		&node.Secrets.CookieSigningKey, &node.Secrets.CSRFTokenSecret,
		&node.Secrets.LearnedOrigins, &startedAt, &lastSeen, &stale, &node.Version)
	if err != nil {
		return Node{}, Classify(err)
	}
	node.StartedAt = time.Unix(startedAt, 0).UTC()
	node.LastSeen = time.Unix(lastSeen, 0).UTC()
	node.Stale = stale != 0
	node.State = DeriveState(node.LastSeen, node.Stale, now)
	return node, nil
}

// DeriveState computes a node's state from its last heartbeat and stale flag
// per AI.md PART 10 -> "Cluster Heartbeat & Failure Handling".
func DeriveState(lastSeen time.Time, stale bool, now time.Time) NodeState {
	age := now.Sub(lastSeen)
	switch {
	case age >= OfflineAfter:
		return StateOffline
	case stale:
		return StateStale
	case age >= DegradedAfter:
		return StateDegraded
	default:
		return StateHealthy
	}
}

// MarkStale sets or clears a node's stale flag, used when the primary
// detects secret-version drift beyond the rotation grace window.
func (db *DB) MarkStale(ctx context.Context, nodeID string, stale bool) error {
	flag := 0
	if stale {
		flag = 1
	}
	_, err := db.ExecContext(ctx, TimeoutWrite,
		`UPDATE nodes SET stale = ?, version = version + 1 WHERE node_id = ?`, flag, nodeID)
	return err
}

// RemoveNode deletes a node record as part of the operator-driven removal
// flow (PART 10 -> "Removed-Node Local Cleanup"). Its primary lease, if it
// held one, is released in the same transaction so election can proceed.
func (db *DB) RemoveNode(ctx context.Context, nodeID string) error {
	return db.Tx(ctx, TimeoutWrite, func(tx *sql.Tx) error {
		if _, err := db.TxExec(ctx, tx,
			`DELETE FROM cluster_locks WHERE lock_name = ? AND owner = ?`,
			PrimaryLockName, nodeID); err != nil {
			return err
		}
		_, err := db.TxExec(ctx, tx, `DELETE FROM nodes WHERE node_id = ?`, nodeID)
		return err
	})
}

// HealthyNodes returns the ids of nodes whose heartbeat is fresh and which
// are not flagged stale.
func (db *DB) HealthyNodes(ctx context.Context) ([]string, error) {
	cutoff := time.Now().UTC().Add(-DegradedAfter).Unix()
	rows, err := db.QueryContext(ctx, TimeoutSelect,
		`SELECT node_id FROM nodes WHERE last_seen >= ? AND stale = 0 ORDER BY node_id`, cutoff)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, Classify(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, Classify(err)
	}
	return ids, nil
}

// HasQuorum reports whether a majority of the registered nodes are healthy.
// A cluster with no registered nodes (a fresh single instance) has quorum.
func (db *DB) HasQuorum(ctx context.Context) (bool, error) {
	var total int
	if err := db.QueryRowContext(ctx, TimeoutSelect,
		`SELECT COUNT(*) FROM nodes`).Scan(&total); err != nil {
		return false, Classify(err)
	}
	if total == 0 {
		return true, nil
	}

	healthy, err := db.HealthyNodes(ctx)
	if err != nil {
		return false, err
	}
	return len(healthy) >= total/2+1, nil
}

// AcquireLock takes the named distributed lock for ttl. It returns
// ErrLockHeld when another node currently owns it.
func (db *DB) AcquireLock(ctx context.Context, name, owner string, ttl time.Duration) (*Lock, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("cluster: lock requires a name")
	}
	if ttl <= 0 {
		ttl = PrimaryLeaseTTL
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	expires := now.Add(ttl)

	err = db.Tx(ctx, TimeoutWrite, func(tx *sql.Tx) error {
		// Clear the row first if the previous holder's lease has lapsed.
		if _, err := db.TxExec(ctx, tx,
			`DELETE FROM cluster_locks WHERE lock_name = ? AND expires_at <= ?`,
			name, now.Unix()); err != nil {
			return err
		}
		_, err := db.TxExec(ctx, tx,
			`INSERT INTO cluster_locks (lock_name, owner, token, acquired_at, expires_at)
			 VALUES (?, ?, ?, ?, ?)`,
			name, owner, token, now.Unix(), expires.Unix())
		if err != nil && isDuplicateRow(err) {
			return ErrLockHeld
		}
		return err
	})
	if err != nil {
		return nil, err
	}

	return &Lock{db: db, Name: name, Owner: owner, Token: token, ExpiresAt: expires}, nil
}

// Refresh extends the lock's lease. It returns ErrLockHeld if the lock has
// already lapsed and been taken over.
func (l *Lock) Refresh(ctx context.Context, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = PrimaryLeaseTTL
	}
	now := time.Now().UTC()
	expires := now.Add(ttl)

	res, err := l.db.ExecContext(ctx, TimeoutWrite,
		`UPDATE cluster_locks SET expires_at = ?
		 WHERE lock_name = ? AND token = ? AND expires_at > ?`,
		expires.Unix(), l.Name, l.Token, now.Unix())
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return Classify(err)
	}
	if rows == 0 {
		return ErrLockHeld
	}
	l.ExpiresAt = expires
	return nil
}

// Release drops the lock. Releasing a lock that already lapsed is a no-op.
func (l *Lock) Release(ctx context.Context) error {
	_, err := l.db.ExecContext(ctx, TimeoutWrite,
		`DELETE FROM cluster_locks WHERE lock_name = ? AND token = ?`, l.Name, l.Token)
	return err
}

// WithLock runs fn while holding the named lock, releasing it afterwards.
// It returns ErrLockHeld without running fn when another node holds the lock,
// which is how duplicate scheduled-task execution is prevented.
func (db *DB) WithLock(ctx context.Context, name, owner string, ttl time.Duration, fn func(context.Context) error) error {
	lock, err := db.AcquireLock(ctx, name, owner, ttl)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release(ctx) }()
	return fn(ctx)
}

// ElectPrimary renews or claims the primary lease for nodeID and reports
// whether this node is the primary. A live lease is never preempted: only
// once it lapses may the lowest-id healthy node claim it.
func (db *DB) ElectPrimary(ctx context.Context, nodeID string) (bool, error) {
	if strings.TrimSpace(nodeID) == "" {
		return false, errors.New("cluster: election requires a node id")
	}
	now := time.Now().UTC()
	expires := now.Add(PrimaryLeaseTTL)
	primary := false

	err := db.Tx(ctx, TimeoutWrite, func(tx *sql.Tx) error {
		primary = false

		var (
			owner     string
			expiresAt int64
		)
		err := db.TxQueryRow(ctx, tx,
			`SELECT owner, expires_at FROM cluster_locks WHERE lock_name = ?`,
			PrimaryLockName).Scan(&owner, &expiresAt)
		switch {
		case err == nil && expiresAt > now.Unix():
			if owner != nodeID {
				return nil
			}
			// Incumbent renewal.
			if _, err := db.TxExec(ctx, tx,
				`UPDATE cluster_locks SET expires_at = ? WHERE lock_name = ? AND owner = ?`,
				expires.Unix(), PrimaryLockName, nodeID); err != nil {
				return err
			}
			primary = true
			return nil
		case err != nil && !IsNotFound(err):
			return Classify(err)
		}

		eligible, err := db.lowestHealthyNode(ctx, tx, now)
		if err != nil {
			return err
		}
		if eligible != "" && eligible != nodeID {
			return nil
		}

		if _, err := db.TxExec(ctx, tx,
			`DELETE FROM cluster_locks WHERE lock_name = ?`, PrimaryLockName); err != nil {
			return err
		}
		token, err := randomToken()
		if err != nil {
			return err
		}
		if _, err := db.TxExec(ctx, tx,
			`INSERT INTO cluster_locks (lock_name, owner, token, acquired_at, expires_at)
			 VALUES (?, ?, ?, ?, ?)`,
			PrimaryLockName, nodeID, token, now.Unix(), expires.Unix()); err != nil {
			if isDuplicateRow(err) {
				return nil
			}
			return err
		}
		primary = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return primary, nil
}

// PrimaryID returns the node id holding a live primary lease, or an empty
// string when the cluster currently has no primary.
func (db *DB) PrimaryID(ctx context.Context) (string, error) {
	var (
		owner     string
		expiresAt int64
	)
	err := db.QueryRowContext(ctx, TimeoutSelect,
		`SELECT owner, expires_at FROM cluster_locks WHERE lock_name = ?`,
		PrimaryLockName).Scan(&owner, &expiresAt)
	if IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", Classify(err)
	}
	if expiresAt <= time.Now().UTC().Unix() {
		return "", nil
	}
	return owner, nil
}

// IsPrimary reports whether nodeID currently holds a live primary lease.
func (db *DB) IsPrimary(ctx context.Context, nodeID string) (bool, error) {
	owner, err := db.PrimaryID(ctx)
	if err != nil {
		return false, err
	}
	return owner != "" && owner == nodeID, nil
}

// lowestHealthyNode returns the smallest node id among healthy nodes, or an
// empty string when no node has registered a fresh heartbeat yet.
func (db *DB) lowestHealthyNode(ctx context.Context, tx *sql.Tx, now time.Time) (string, error) {
	cutoff := now.Add(-DegradedAfter).Unix()
	var lowest sql.NullString
	err := db.TxQueryRow(ctx, tx,
		`SELECT MIN(node_id) FROM nodes WHERE last_seen >= ? AND stale = 0`, cutoff).Scan(&lowest)
	if err != nil && !IsNotFound(err) {
		return "", Classify(err)
	}
	if !lowest.Valid {
		return "", nil
	}
	return lowest.String, nil
}

// randomToken produces the opaque ownership token stored with a lock.
func randomToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("cluster: generate lock token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// isDuplicateRow reports whether an INSERT failed because the primary key
// already exists, across every supported driver.
func isDuplicateRow(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	needles := []string{
		"unique constraint",
		"duplicate entry",
		"duplicate key",
		"violation of primary key",
	}
	for _, needle := range needles {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
