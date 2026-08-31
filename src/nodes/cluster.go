package nodes

import (
	"context"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

// ControlPlane is a capability handle over the shared control plane. Every
// cluster primitive this package exposes is a method on this type and on no
// other, and the only constructor is Service.ControlPlane, which refuses any
// node whose stored role is not RoleCluster.
//
// That is what makes the cluster/managed split structural rather than a
// convention: a caller holding only a *Service has no method that heartbeats
// into the control plane, takes a cluster lock, stands for election or reads
// control-plane membership. To reach one it must first present a node id and
// be handed a *ControlPlane back, and a managed node never is.
type ControlPlane struct {
	svc    *Service
	nodeID string
}

// ControlPlane returns the control-plane handle for a cluster node.
//
// It returns ErrNotClusterNode when the node's stored role is RoleManaged, and
// ErrInvalidTransition once the node has been removed. Because Role is
// immutable after enrollment there is no sequence of calls that turns a
// managed node into a cluster one, so this check cannot be raced.
func (s *Service) ControlPlane(ctx context.Context, nodeID string) (*ControlPlane, error) {
	node, err := s.Get(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return s.controlPlaneFor(node)
}

// ControlPlaneFor returns the control-plane handle for an already
// authenticated identity, applying the same role gate as ControlPlane
// without a second database read.
func (s *Service) ControlPlaneFor(id Identity) (*ControlPlane, error) {
	return s.controlPlaneFor(id.Node)
}

// controlPlaneFor is the single place a *ControlPlane is constructed. Every
// entry point funnels through it so the role gate cannot be bypassed by
// adding another accessor later.
func (s *Service) controlPlaneFor(node Node) (*ControlPlane, error) {
	if node.Role != RoleCluster {
		return nil, ErrNotClusterNode
	}
	if node.State == StateRemoved {
		return nil, ErrInvalidTransition
	}
	return &ControlPlane{svc: s, nodeID: node.ID}, nil
}

// NodeID returns the cluster node this handle speaks for.
func (c *ControlPlane) NodeID() string { return c.nodeID }

// Heartbeat writes this node's control-plane heartbeat. The node id is taken
// from the handle, never from the caller, so one cluster node cannot
// heartbeat on behalf of another.
func (c *ControlPlane) Heartbeat(ctx context.Context, hb database.Heartbeat) error {
	hb.NodeID = c.nodeID
	if err := c.svc.cluster.Heartbeat(ctx, hb); err != nil {
		return wrapInternal(err, "cluster heartbeat")
	}
	return nil
}

// Members returns every control-plane member. Only a cluster node can reach
// this, so managed-node code paths never observe control-plane state.
func (c *ControlPlane) Members(ctx context.Context) ([]database.Node, error) {
	members, err := c.svc.cluster.Nodes(ctx)
	if err != nil {
		return nil, wrapInternal(err, "list cluster members")
	}
	return members, nil
}

// HealthyMembers returns the ids of members with a fresh heartbeat.
func (c *ControlPlane) HealthyMembers(ctx context.Context) ([]string, error) {
	ids, err := c.svc.cluster.HealthyNodes(ctx)
	if err != nil {
		return nil, wrapInternal(err, "list healthy cluster members")
	}
	return ids, nil
}

// HasQuorum reports whether a majority of members are healthy.
func (c *ControlPlane) HasQuorum(ctx context.Context) (bool, error) {
	ok, err := c.svc.cluster.HasQuorum(ctx)
	if err != nil {
		return false, wrapInternal(err, "check cluster quorum")
	}
	return ok, nil
}

// AcquireLock takes a named cluster lock owned by this node.
func (c *ControlPlane) AcquireLock(ctx context.Context, name string, ttl time.Duration) (*database.Lock, error) {
	lock, err := c.svc.cluster.AcquireLock(ctx, name, c.nodeID, ttl)
	if err != nil {
		return nil, wrapInternal(err, "acquire cluster lock")
	}
	return lock, nil
}

// WithLock runs fn while this node holds a named cluster lock.
func (c *ControlPlane) WithLock(ctx context.Context, name string, ttl time.Duration, fn func(context.Context) error) error {
	return c.svc.cluster.WithLock(ctx, name, c.nodeID, ttl, fn)
}

// ElectPrimary claims or renews the primary lease for this node and reports
// whether it now holds it.
func (c *ControlPlane) ElectPrimary(ctx context.Context) (bool, error) {
	won, err := c.svc.cluster.ElectPrimary(ctx, c.nodeID)
	if err != nil {
		return false, wrapInternal(err, "elect cluster primary")
	}
	return won, nil
}

// IsPrimary reports whether this node currently holds the primary lease.
func (c *ControlPlane) IsPrimary(ctx context.Context) (bool, error) {
	ok, err := c.svc.cluster.IsPrimary(ctx, c.nodeID)
	if err != nil {
		return false, wrapInternal(err, "check cluster primary")
	}
	return ok, nil
}

// PrimaryID returns the id of the current primary, or an empty string when
// no lease is held.
func (c *ControlPlane) PrimaryID(ctx context.Context) (string, error) {
	id, err := c.svc.cluster.PrimaryID(ctx)
	if err != nil {
		return "", wrapInternal(err, "read cluster primary")
	}
	return id, nil
}

// ElectionEligible reports whether a node may stand for primary election.
// Only a cluster node that is enrolled and not draining or removed is
// eligible; a managed node never is, whatever its liveness state.
func ElectionEligible(n Node) bool {
	if n.Role != RoleCluster {
		return false
	}
	switch n.State {
	case StateEnrolled, StateOnline, StateDegraded:
		return !n.Maintenance
	default:
		return false
	}
}
