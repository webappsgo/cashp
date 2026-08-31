package nodes

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

// allowedTransitions is the node lifecycle state machine. A transition that
// is not listed here is rejected with ErrInvalidTransition; there is no
// fallback branch that lets an unexpected pair through.
//
//	pending  -> enrolled | removed
//	enrolled -> online | degraded | offline | drained | removed
//	online   -> degraded | offline | drained | enrolled | removed
//	degraded -> online | offline | drained | enrolled | removed
//	offline  -> online | degraded | drained | enrolled | removed
//	drained  -> enrolled | online | removed
//	removed  -> (terminal)
//
// The transitions back to enrolled are what a rejoin or a re-key produces:
// the node record survives, its old credential is retired and it re-enters
// the lifecycle at enrolled.
var allowedTransitions = map[State]map[State]bool{
	StatePending: {
		StateEnrolled: true,
		StateRemoved:  true,
	},
	StateEnrolled: {
		StateOnline:   true,
		StateDegraded: true,
		StateOffline:  true,
		StateDrained:  true,
		StateEnrolled: true,
		StateRemoved:  true,
	},
	StateOnline: {
		StateDegraded: true,
		StateOffline:  true,
		StateDrained:  true,
		StateEnrolled: true,
		StateRemoved:  true,
	},
	StateDegraded: {
		StateOnline:   true,
		StateOffline:  true,
		StateDrained:  true,
		StateEnrolled: true,
		StateRemoved:  true,
	},
	StateOffline: {
		StateOnline:   true,
		StateDegraded: true,
		StateDrained:  true,
		StateEnrolled: true,
		StateRemoved:  true,
	},
	StateDrained: {
		StateEnrolled: true,
		StateOnline:   true,
		StateRemoved:  true,
	},
	StateRemoved: {},
}

// Valid reports whether s is a defined lifecycle state.
func (s State) Valid() bool {
	_, ok := allowedTransitions[s]
	return ok
}

// CanTransition reports whether the lifecycle permits moving from one state
// to another.
func CanTransition(from, to State) bool {
	next, ok := allowedTransitions[from]
	if !ok {
		return false
	}
	return next[to]
}

// Transition moves a node to a new lifecycle state, recording a short
// operator-supplied reason. It refuses any pair the state machine does not
// allow and refuses removal, which has its own confirmed entry point.
func (s *Service) Transition(ctx context.Context, nodeID string, to State, reason, actor string) (Node, error) {
	if !to.Valid() {
		return Node{}, ErrInvalidTransition
	}
	if to == StateRemoved {
		return Node{}, ErrConfirmationRequired
	}
	return s.transition(ctx, nodeID, to, reason, actor, "nodes.state_changed")
}

// transition applies a state change under optimistic locking and audits it.
func (s *Service) transition(ctx context.Context, nodeID string, to State, reason, actor, event string) (Node, error) {
	node, err := s.Get(ctx, nodeID)
	if err != nil {
		return Node{}, err
	}
	if !CanTransition(node.State, to) {
		return Node{}, ErrInvalidTransition
	}

	from := node.State
	now := s.now()
	clean := truncate(reason, MaxReasonLen)

	if err := s.db.UpdateVersioned(ctx,
		`UPDATE node_registry SET state = ?, state_reason = ?, state_changed_at = ?,
			updated_at = ?, version = version + 1
		 WHERE id = ? AND version = ?`,
		string(to), clean, unixOf(now), unixOf(now), node.ID, node.Version); err != nil {
		return Node{}, wrapVersioned(err, "update node state")
	}

	node.State = to
	node.StateReason = clean
	node.StateChangedAt = now
	node.UpdatedAt = now
	node.Version++

	s.audit(event, truncate(actor, MaxNodeNameLen), node.ID,
		"role", string(node.Role), "from", string(from), "to", string(to), "reason", clean)
	return node, nil
}

// RecordContact registers an authenticated node's liveness. It refreshes
// last_seen and, unless the node is in maintenance or drained, brings it
// back to online. It is the single liveness entry point for both roles: a
// cluster node additionally heartbeats into the control plane through its
// *ControlPlane handle, which this call deliberately does not do for it.
func (s *Service) RecordContact(ctx context.Context, id Identity, agentVersion string) (Node, error) {
	node := id.Node
	now := s.now()
	// AI.md PART 11 "Defense-in-Depth Layers": reject control chars / null
	// bytes at input — checked against the raw trimmed value, before
	// truncate() strips control characters, so a hostile version string is
	// rejected outright instead of being silently sanitized and accepted.
	trimmed := strings.TrimSpace(agentVersion)
	if trimmed != "" && !isPrintableToken(trimmed) {
		return Node{}, ErrInvalidFacts
	}
	version := truncate(agentVersion, MaxVersionLen)
	if version == "" {
		version = node.AgentVersion
	}

	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE node_registry SET last_seen = ?, agent_version = ?, updated_at = ?,
			version = version + 1
		 WHERE id = ?`,
		unixOf(now), version, unixOf(now), node.ID); err != nil {
		return Node{}, wrapInternal(err, "record node contact")
	}
	node.LastSeen = now
	node.AgentVersion = version
	node.UpdatedAt = now
	node.Version++

	if node.Maintenance || node.State == StateDrained || node.State == StateOnline {
		return node, nil
	}
	if !CanTransition(node.State, StateOnline) {
		return node, nil
	}
	return s.transition(ctx, node.ID, StateOnline, "contact", "node", "nodes.online")
}

// Cordon excludes a node from new dispatches without changing its lifecycle
// state. Work already queued for it stays queued.
func (s *Service) Cordon(ctx context.Context, nodeID, reason, actor string) (Node, error) {
	return s.setFlags(ctx, nodeID, flagUpdate{cordoned: boolPtr(true)}, reason, actor, "nodes.cordoned")
}

// Uncordon lets a node receive new dispatches again.
func (s *Service) Uncordon(ctx context.Context, nodeID, actor string) (Node, error) {
	return s.setFlags(ctx, nodeID, flagUpdate{cordoned: boolPtr(false)}, "", actor, "nodes.uncordoned")
}

// SetMaintenance marks or clears a planned maintenance window. A node in
// maintenance receives no new work and its liveness transitions are
// suppressed, so a planned reboot does not raise a false alarm.
func (s *Service) SetMaintenance(ctx context.Context, nodeID string, on bool, reason, actor string) (Node, error) {
	event := "nodes.maintenance_cleared"
	if on {
		event = "nodes.maintenance_started"
	}
	return s.setFlags(ctx, nodeID, flagUpdate{maintenance: boolPtr(on)}, reason, actor, event)
}

// flagUpdate carries the optional boolean flags a scheduling change writes.
type flagUpdate struct {
	// cordoned sets the cordon flag when non-nil.
	cordoned *bool
	// maintenance sets the maintenance flag when non-nil.
	maintenance *bool
}

// boolPtr returns a pointer to b, so a flag update can distinguish "set to
// false" from "leave alone".
func boolPtr(b bool) *bool { return &b }

// setFlags applies a scheduling-flag change under optimistic locking.
func (s *Service) setFlags(ctx context.Context, nodeID string, upd flagUpdate, reason, actor, event string) (Node, error) {
	node, err := s.Get(ctx, nodeID)
	if err != nil {
		return Node{}, err
	}
	if node.State == StateRemoved {
		return Node{}, ErrInvalidTransition
	}

	cordoned := node.Cordoned
	if upd.cordoned != nil {
		cordoned = *upd.cordoned
	}
	maintenance := node.Maintenance
	if upd.maintenance != nil {
		maintenance = *upd.maintenance
	}

	now := s.now()
	clean := truncate(reason, MaxReasonLen)
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE node_registry SET cordoned = ?, maintenance = ?, state_reason = ?,
			updated_at = ?, version = version + 1
		 WHERE id = ? AND version = ?`,
		boolInt(cordoned), boolInt(maintenance), clean, unixOf(now), node.ID, node.Version); err != nil {
		return Node{}, wrapVersioned(err, "update node scheduling flags")
	}

	node.Cordoned = cordoned
	node.Maintenance = maintenance
	node.StateReason = clean
	node.UpdatedAt = now
	node.Version++

	s.audit(event, truncate(actor, MaxNodeNameLen), node.ID,
		"cordoned", cordoned, "maintenance", maintenance, "reason", clean)
	return node, nil
}

// DrainResult reports what a drain did.
type DrainResult struct {
	// Node is the node after the drain.
	Node Node
	// CancelledTasks is how many queued tasks were cancelled.
	CancelledTasks int64
}

// Drain cordons a node, cancels the work still queued for it and moves it to
// the drained state. Tasks already claimed by the node are left alone so a
// running operation is not orphaned mid-flight; the reaper handles them if
// they never report.
func (s *Service) Drain(ctx context.Context, nodeID, reason, actor string) (DrainResult, error) {
	node, err := s.Get(ctx, nodeID)
	if err != nil {
		return DrainResult{}, err
	}
	if !CanTransition(node.State, StateDrained) {
		return DrainResult{}, ErrInvalidTransition
	}

	if _, err := s.setFlags(ctx, nodeID, flagUpdate{cordoned: boolPtr(true)}, reason, actor, "nodes.cordoned"); err != nil {
		return DrainResult{}, err
	}
	cancelled, err := s.cancelQueued(ctx, nodeID, "node drained")
	if err != nil {
		return DrainResult{}, err
	}
	drained, err := s.transition(ctx, nodeID, StateDrained, reason, actor, "nodes.drained")
	if err != nil {
		return DrainResult{}, err
	}

	s.audit("nodes.drain_completed", truncate(actor, MaxNodeNameLen), nodeID,
		"cancelled_tasks", cancelled)
	return DrainResult{Node: drained, CancelledTasks: cancelled}, nil
}

// RemoveRequest asks for a node to be retired.
type RemoveRequest struct {
	// NodeID is the node to remove. Required.
	NodeID string
	// Confirm must be true. Removal irreversibly retires the node's
	// credentials, so it is never the default outcome of a request that
	// merely reaches the endpoint.
	Confirm bool
	// Reason is the operator's short justification, recorded in the audit
	// log.
	Reason string
	// Actor is the administrator performing the removal.
	Actor string
}

// Remove retires a node: it revokes every credential and enrollment token,
// cancels the work still queued for it, drops a cluster node from the
// control-plane membership table and moves the record to the terminal
// removed state. The registry row itself is retained so the audit trail
// keeps resolving.
func (s *Service) Remove(ctx context.Context, req RemoveRequest) (Node, error) {
	if !req.Confirm {
		return Node{}, ErrConfirmationRequired
	}
	node, err := s.Get(ctx, req.NodeID)
	if err != nil {
		return Node{}, err
	}
	if !CanTransition(node.State, StateRemoved) {
		return Node{}, ErrInvalidTransition
	}

	now := s.now()
	if err := s.db.Tx(ctx, database.TimeoutWrite, func(tx *sql.Tx) error {
		if _, err := s.db.TxExec(ctx, tx,
			`UPDATE node_credentials SET revoked_at = ? WHERE node_id = ? AND revoked_at = 0`,
			unixOf(now), node.ID); err != nil {
			return wrapInternal(err, "revoke node credentials")
		}
		if _, err := s.db.TxExec(ctx, tx,
			`UPDATE node_enrollment_tokens SET revoked_at = ? WHERE node_id = ? AND revoked_at = 0`,
			unixOf(now), node.ID); err != nil {
			return wrapInternal(err, "revoke node enrollment tokens")
		}
		return nil
	}); err != nil {
		return Node{}, err
	}

	if _, err := s.cancelQueued(ctx, node.ID, "node removed"); err != nil {
		return Node{}, err
	}

	// Only a cluster node ever had a control-plane membership row, so only a
	// cluster node has one to drop.
	if node.Role == RoleCluster {
		if err := s.cluster.RemoveNode(ctx, node.ID); err != nil {
			return Node{}, wrapInternal(err, "remove cluster member")
		}
	}

	removed, err := s.transition(ctx, node.ID, StateRemoved, req.Reason, req.Actor, "nodes.removed")
	if err != nil {
		return Node{}, err
	}
	return removed, nil
}

// SweepResult reports what a liveness sweep changed.
type SweepResult struct {
	// Degraded is how many nodes moved to degraded.
	Degraded int
	// Offline is how many nodes moved to offline.
	Offline int
}

// SweepLiveness reclassifies nodes from their last contact using the
// thresholds AI.md PART 10 defines and src/database exports: silent for
// DegradedAfter is degraded, silent for OfflineAfter is offline, and removal
// stays manual. Nodes in maintenance, drained nodes and removed nodes are
// left alone.
func (s *Service) SweepLiveness(ctx context.Context) (SweepResult, error) {
	nodes, err := s.List(ctx, "")
	if err != nil {
		return SweepResult{}, err
	}

	now := s.now()
	var result SweepResult
	for _, node := range nodes {
		if node.Maintenance {
			continue
		}
		switch node.State {
		case StateOnline, StateDegraded, StateEnrolled:
		default:
			continue
		}

		reference := node.LastSeen
		if reference.IsZero() {
			reference = node.EnrolledAt
		}
		if reference.IsZero() {
			continue
		}
		age := now.Sub(reference)

		var want State
		switch {
		case age >= database.OfflineAfter:
			want = StateOffline
		case age >= database.DegradedAfter:
			want = StateDegraded
		default:
			continue
		}
		if node.State == want {
			continue
		}
		if _, err := s.transition(ctx, node.ID, want, "liveness sweep", "scheduler", "nodes.liveness"); err != nil {
			return result, err
		}
		if want == StateOffline {
			result.Offline++
			continue
		}
		result.Degraded++
	}
	return result, nil
}

// LastContact returns how long a node has been silent at the given time.
func LastContact(n Node, now time.Time) time.Duration {
	if n.LastSeen.IsZero() {
		return 0
	}
	return now.Sub(n.LastSeen)
}
