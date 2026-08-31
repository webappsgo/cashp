// Package nodes implements cashp's fleet node model and the strict
// separation AI.md draws between the two kinds of node a cashp installation
// knows about.
//
// A Cluster Node is another instance of THIS server binary (AI.md PART 1
// "Cluster vs Managed Nodes", PART 10 "Cluster nodes vs agents"). It shares
// the control-plane database and cache, heartbeats into the nodes table,
// competes for cluster locks, is eligible for primary election and therefore
// eligible to run cluster-wide scheduled tasks.
//
// A Managed Node is an EXTERNAL machine the control plane drives remotely
// through the cashp-agent binary (AI.md PART 33). It never shares the
// control-plane database, never receives app_secrets, never acquires a
// cluster lock and is never election-eligible. Everything it reports is
// untrusted input: it is validated, size-bounded and stored through
// parameterized statements only.
//
// The separation is enforced structurally rather than by convention: the
// cluster primitives in src/database are reachable only through the
// *ControlPlane handle returned by Service.ControlPlane, and that handle is
// only ever constructed for a node whose stored role is RoleCluster.
package nodes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/database"
	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/logging"
	"github.com/webappsgo/cashp/src/security"
)

// Role is the kind of node a record describes. It is assigned when the
// enrollment token is issued and is immutable for the life of the node:
// there is deliberately no API that changes a node's role, so a managed
// node can never be promoted into the control plane.
type Role string

// The two node roles defined by AI.md PART 1 -> "Cluster vs Managed Nodes".
const (
	// RoleCluster is another instance of this server binary sharing the
	// control-plane database and cache.
	RoleCluster Role = "cluster"
	// RoleManaged is an external machine driven by the control plane over
	// the authenticated agent channel.
	RoleManaged Role = "managed"
)

// Valid reports whether r is one of the two defined roles.
func (r Role) Valid() bool { return r == RoleCluster || r == RoleManaged }

// State is a node's lifecycle state.
type State string

// Node lifecycle states. The liveness states (online, degraded, offline)
// mirror the thresholds AI.md PART 10 defines for cluster heartbeats, which
// src/database exposes as DegradedAfter and OfflineAfter.
const (
	// StatePending is a node record created but not yet enrolled.
	StatePending State = "pending"
	// StateEnrolled is a node that redeemed an enrollment token and has not
	// yet reported liveness.
	StateEnrolled State = "enrolled"
	// StateOnline is a node whose last contact is inside DegradedAfter.
	StateOnline State = "online"
	// StateDegraded is a node silent for at least DegradedAfter.
	StateDegraded State = "degraded"
	// StateOffline is a node silent for at least OfflineAfter.
	StateOffline State = "offline"
	// StateDrained is a node an operator drained; it holds no queued work
	// and receives no new dispatches.
	StateDrained State = "drained"
	// StateRemoved is the terminal state; the record is retained for audit
	// and its credentials are revoked.
	StateRemoved State = "removed"
)

// Defaults for enrollment and dispatch. They are package-level so the admin
// panel can present them and so tests can reason about them without magic
// numbers.
const (
	// DefaultEnrollmentTTL is how long an unused enrollment token stays
	// redeemable.
	DefaultEnrollmentTTL = time.Hour
	// MaxEnrollmentTTL bounds an operator-supplied enrollment token TTL.
	MaxEnrollmentTTL = 7 * 24 * time.Hour
	// DefaultEnrollmentUses is the use budget of an enrollment token when
	// the caller does not set one: single use.
	DefaultEnrollmentUses = 1
	// MaxEnrollmentUses bounds a bounded-use enrollment token.
	MaxEnrollmentUses = 64
	// DefaultTaskTimeout is how long a dispatched task may stay claimed
	// before the reaper treats it as timed out.
	DefaultTaskTimeout = 5 * time.Minute
	// MaxTaskTimeout bounds a caller-supplied task timeout.
	MaxTaskTimeout = time.Hour
	// DefaultMaxAttempts is the delivery budget of a dispatched task.
	DefaultMaxAttempts = 3
	// MaxTaskAttempts bounds a caller-supplied attempt budget.
	MaxTaskAttempts = 10
	// RetryBaseDelay is the first retry delay; each further attempt doubles
	// it up to RetryMaxDelay.
	RetryBaseDelay = 5 * time.Second
	// RetryMaxDelay caps the exponential retry backoff.
	RetryMaxDelay = 5 * time.Minute
	// NotifyTimeout bounds a wake-up push to a node's callback URL.
	NotifyTimeout = 10 * time.Second
	// MaxNotifyResponseBytes bounds how much of a node's HTTP response is
	// read, since a node is untrusted and may answer with an endless body.
	MaxNotifyResponseBytes = 4 << 10
)

// Token prefixes carried by node credentials. Both are prefixes AI.md
// PART 11 already defines; this package introduces none of its own. A
// cluster node joins with an admin-scope credential, a managed node with an
// admin-scope agent credential, so the prefix alone already distinguishes
// the two channels before any database lookup happens.
const (
	// ClusterTokenPrefix is the prefix of a cluster node's credential.
	ClusterTokenPrefix = security.PrefixAdmin
	// ManagedTokenPrefix is the prefix of a managed node's credential.
	ManagedTokenPrefix = security.PrefixAdminAgent
)

// TokenPrefixFor returns the credential prefix used by a role.
func TokenPrefixFor(role Role) string {
	if role == RoleCluster {
		return ClusterTokenPrefix
	}
	return ManagedTokenPrefix
}

// Node is a fleet member as stored in node_registry.
type Node struct {
	// ID is the stable node identifier, validated against a strict allowlist.
	ID string
	// Name is the operator-facing display name.
	Name string
	// Role is cluster or managed and never changes after enrollment.
	Role Role
	// State is the lifecycle state.
	State State
	// StateReason is the short reason recorded with the last transition.
	StateReason string
	// Address is the reachable host:port a node reported, informational only.
	Address string
	// CallbackURL is the optional wake-up endpoint on a managed node. It is
	// always SSRF-validated before it is stored and again before it is used.
	CallbackURL string
	// AgentVersion is the version the node last reported.
	AgentVersion string
	// Cordoned excludes the node from new dispatches without changing state.
	Cordoned bool
	// Maintenance marks a planned maintenance window; like Cordoned it stops
	// new dispatches, and it additionally suppresses liveness transitions.
	Maintenance bool
	// EnrolledAt is when the node last redeemed an enrollment token.
	EnrolledAt time.Time
	// LastSeen is the node's last authenticated contact.
	LastSeen time.Time
	// StateChangedAt is when State was last written.
	StateChangedAt time.Time
	// CreatedAt is when the record was created.
	CreatedAt time.Time
	// UpdatedAt is when the record was last written.
	UpdatedAt time.Time
	// Version is the optimistic-locking row version.
	Version int64
}

// Schedulable reports whether the node may receive newly dispatched work.
func (n Node) Schedulable() bool {
	if n.Cordoned || n.Maintenance {
		return false
	}
	switch n.State {
	case StateEnrolled, StateOnline, StateDegraded:
		return true
	default:
		return false
	}
}

// Doer is the narrow HTTP contract this package needs, so tests can inject
// a fake transport and never open a socket.
type Doer interface {
	// Do performs the request and returns the response.
	Do(req *http.Request) (*http.Response, error)
}

// ClusterOps is the subset of the src/database cluster primitives this
// package builds on. Declaring it here keeps the dependency narrow and lets
// tests assert that a managed node never reaches any of these calls.
// *database.DB satisfies it.
type ClusterOps interface {
	// Heartbeat registers or refreshes a cluster node's control-plane row.
	Heartbeat(ctx context.Context, hb database.Heartbeat) error
	// Nodes returns every control-plane member.
	Nodes(ctx context.Context) ([]database.Node, error)
	// HealthyNodes returns the ids of members with a fresh heartbeat.
	HealthyNodes(ctx context.Context) ([]string, error)
	// HasQuorum reports whether a majority of members are healthy.
	HasQuorum(ctx context.Context) (bool, error)
	// AcquireLock takes a named distributed lock.
	AcquireLock(ctx context.Context, name, owner string, ttl time.Duration) (*database.Lock, error)
	// WithLock runs fn while holding a named distributed lock.
	WithLock(ctx context.Context, name, owner string, ttl time.Duration, fn func(context.Context) error) error
	// ElectPrimary renews or claims the primary lease.
	ElectPrimary(ctx context.Context, nodeID string) (bool, error)
	// IsPrimary reports whether the node holds a live primary lease.
	IsPrimary(ctx context.Context, nodeID string) (bool, error)
	// PrimaryID returns the current primary, or an empty string.
	PrimaryID(ctx context.Context) (string, error)
	// RemoveNode deletes a control-plane member record.
	RemoveNode(ctx context.Context, nodeID string) error
}

// Options configures a Service.
type Options struct {
	// DB is the control-plane database holding this package's tables.
	DB *database.DB
	// Cluster supplies the cluster primitives. Nil uses DB.
	Cluster ClusterOps
	// Now supplies the current time. Nil uses time.Now, and every stored
	// timestamp is UTC.
	Now func() time.Time
	// HTTP performs node-bound requests. Nil disables outbound wake-up
	// pushes entirely rather than silently building a default client.
	HTTP Doer
	// CredentialTTL is how long an issued node credential stays valid. Zero
	// issues a credential that does not expire on its own and is retired by
	// re-keying or removal instead.
	CredentialTTL time.Duration
	// Actions are the dispatchable actions registered at construction time,
	// in addition to the core actions this package owns.
	Actions []ActionSpec
}

// Service is the node subsystem. It is safe for concurrent use.
type Service struct {
	db      *database.DB
	cluster ClusterOps
	now     func() time.Time
	http    Doer
	credTTL time.Duration

	mu      sync.RWMutex
	actions map[string]ActionSpec
}

// New builds a Service. It fails when the database handle is missing or an
// action definition is invalid; there is no partially configured Service.
func New(opts Options) (*Service, error) {
	if opts.DB == nil {
		return nil, apperr.New(apperr.CodeInternal, 0, "Node service is unavailable").
			WithCause(fmt.Errorf("nodes: Options.DB is required"))
	}
	cluster := opts.Cluster
	if cluster == nil {
		cluster = opts.DB
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	if opts.CredentialTTL < 0 {
		return nil, apperr.New(apperr.CodeValidation, 0, "Credential lifetime is invalid")
	}

	s := &Service{
		db:      opts.DB,
		cluster: cluster,
		now:     func() time.Time { return now().UTC() },
		http:    opts.HTTP,
		credTTL: opts.CredentialTTL,
		actions: make(map[string]ActionSpec, len(coreActions)+len(opts.Actions)),
	}
	for _, spec := range coreActions {
		if err := s.RegisterAction(spec); err != nil {
			return nil, err
		}
	}
	for _, spec := range opts.Actions {
		if err := s.RegisterAction(spec); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Now returns the service clock, which tests replace to drive expiry and
// retry behaviour without sleeping.
func (s *Service) Now() time.Time { return s.now() }

// Get returns a single node by id.
func (s *Service) Get(ctx context.Context, nodeID string) (Node, error) {
	if err := ValidateNodeID(nodeID); err != nil {
		return Node{}, err
	}
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+nodeColumns+` FROM node_registry WHERE id = ?`, nodeID)
	node, err := scanNodeRow(row)
	if database.IsNotFound(err) {
		return Node{}, ErrNodeNotFound
	}
	if err != nil {
		return Node{}, wrapInternal(err, "load node")
	}
	return node, nil
}

// List returns every node, optionally filtered by role, ordered by id. An
// empty role returns both kinds.
func (s *Service) List(ctx context.Context, role Role) ([]Node, error) {
	query := `SELECT ` + nodeColumns + ` FROM node_registry`
	args := []any{}
	if role != "" {
		if !role.Valid() {
			return nil, ErrInvalidRole
		}
		query += ` WHERE role = ?`
		args = append(args, string(role))
	}
	query += ` ORDER BY id`

	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query, args...)
	if err != nil {
		return nil, wrapInternal(err, "list nodes")
	}
	defer func() { _ = rows.Close() }()

	var out []Node
	for rows.Next() {
		node, err := scanNodeRow(rows)
		if err != nil {
			return nil, wrapInternal(err, "scan node")
		}
		out = append(out, node)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapInternal(err, "list nodes")
	}
	return out, nil
}

// nodeColumns is the fixed column list every node read uses. It is a
// constant, never assembled from caller input.
const nodeColumns = `id, name, role, state, state_reason, address, callback_url,
	agent_version, cordoned, maintenance, enrolled_at, last_seen,
	state_changed_at, created_at, updated_at, version`

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	// Scan copies the current row's columns into dest.
	Scan(dest ...any) error
}

// scanNodeRow reads one node_registry row.
func scanNodeRow(sc rowScanner) (Node, error) {
	var (
		node        Node
		role        string
		state       string
		cordoned    int64
		maintenance int64
		enrolledAt  int64
		lastSeen    int64
		changedAt   int64
		createdAt   int64
		updatedAt   int64
	)
	err := sc.Scan(&node.ID, &node.Name, &role, &state, &node.StateReason,
		&node.Address, &node.CallbackURL, &node.AgentVersion, &cordoned,
		&maintenance, &enrolledAt, &lastSeen, &changedAt, &createdAt,
		&updatedAt, &node.Version)
	if err != nil {
		return Node{}, err
	}
	node.Role = Role(role)
	node.State = State(state)
	node.Cordoned = cordoned != 0
	node.Maintenance = maintenance != 0
	node.EnrolledAt = unixOrZero(enrolledAt)
	node.LastSeen = unixOrZero(lastSeen)
	node.StateChangedAt = unixOrZero(changedAt)
	node.CreatedAt = unixOrZero(createdAt)
	node.UpdatedAt = unixOrZero(updatedAt)
	return node, nil
}

// unixOrZero converts a stored Unix second count back to a UTC time,
// mapping the sentinel zero to the zero time.
func unixOrZero(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

// unixOf converts a time to the stored Unix second count, mapping the zero
// time to the sentinel zero.
func unixOf(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}

// boolInt converts a Go bool to the integer column encoding used by every
// supported driver.
func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// newID returns a random opaque identifier for a row this package creates.
func newID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("nodes: generate id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// audit writes one node event to the append-only audit log. Callers pass
// identifiers and outcomes only: no token, credential, payload or command
// line ever reaches this function.
func (s *Service) audit(event, actor, nodeID string, attrs ...any) {
	fields := make([]any, 0, len(attrs)+4)
	fields = append(fields, "category", "nodes", "actor", actor, "target", nodeID)
	fields = append(fields, attrs...)
	logging.Audit().Info(event, fields...)
}

// wrapInternal converts a storage failure into an API-safe error. The cause
// is kept for the log only, so no driver text, DSN or host name can reach a
// client through the message.
func wrapInternal(err error, op string) error {
	if err == nil {
		return nil
	}
	return apperr.Wrap(fmt.Errorf("nodes: %s: %w", op, err), apperr.CodeInternal, 0,
		"Node operation failed")
}

// wrapVersioned converts an optimistic-locking failure into the caller-facing
// conflict error and anything else into an API-safe internal error.
func wrapVersioned(err error, op string) error {
	if errors.Is(err, database.ErrConflict) {
		return ErrConcurrentUpdate
	}
	return wrapInternal(err, op)
}

// truncate bounds an untrusted string to n bytes after control characters
// have been stripped, so a hostile node cannot inflate a row or smuggle
// terminal escapes into an operator's console.
func truncate(s string, n int) string {
	clean := security.StripControlChars(strings.TrimSpace(s))
	if len(clean) <= n {
		return clean
	}
	return clean[:n]
}
