package nodes

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

// TestManagedNodeCannotReachControlPlane is the structural proof the
// cluster/managed split demands: a managed node cannot obtain the handle
// that carries the cluster primitives, so it cannot lock, cannot stand for
// election and cannot read control-plane state. The fake cluster records
// every primitive, so the assertion is not only on the returned error but on
// the fact that nothing was ever dialled.
func TestManagedNodeCannotReachControlPlane(t *testing.T) {
	e := newEnv(t)
	node, credential := e.enroll(t, RoleManaged, "managed-a")

	if _, err := e.svc.ControlPlane(e.ctx, node.ID); !errors.Is(err, ErrNotClusterNode) {
		t.Fatalf("ControlPlane(managed) error = %v, want ErrNotClusterNode", err)
	}

	id, err := e.svc.Authenticate(e.ctx, credential)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if _, err := e.svc.ControlPlaneFor(id); !errors.Is(err, ErrNotClusterNode) {
		t.Fatalf("ControlPlaneFor(managed) error = %v, want ErrNotClusterNode", err)
	}

	if e.cluster.total() != 0 {
		t.Fatalf("managed node reached %d cluster primitives, want 0", e.cluster.total())
	}
	if ElectionEligible(node) {
		t.Fatal("a managed node must never be election eligible")
	}
}

// TestServiceExposesNoClusterPrimitives proves the separation is structural
// rather than a convention: there is no method on *Service that performs a
// cluster operation, so no caller can reach one without first passing the
// role gate in Service.ControlPlane.
func TestServiceExposesNoClusterPrimitives(t *testing.T) {
	forbidden := map[string]bool{
		"AcquireLock":  true,
		"WithLock":     true,
		"ElectPrimary": true,
		"IsPrimary":    true,
		"PrimaryID":    true,
		"HasQuorum":    true,
		"Heartbeat":    true,
		"HealthyNodes": true,
		"Members":      true,
	}
	typ := reflect.TypeOf(&Service{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if forbidden[name] {
			t.Fatalf("*Service exposes cluster primitive %q outside the role gate", name)
		}
	}
}

func TestClusterNodeUsesControlPlane(t *testing.T) {
	e := newEnv(t)
	node, credential := e.enroll(t, RoleCluster, "cluster-a")

	cp, err := e.svc.ControlPlane(e.ctx, node.ID)
	if err != nil {
		t.Fatalf("ControlPlane: %v", err)
	}
	if cp.NodeID() != node.ID {
		t.Fatalf("NodeID = %q", cp.NodeID())
	}

	// The handle always speaks for its own node: a caller-supplied node id in
	// the heartbeat payload is overwritten.
	if err := cp.Heartbeat(e.ctx, database.Heartbeat{NodeID: "someone-else", Address: "10.9.9.9:9443"}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if e.cluster.callCount("Heartbeat") != 1 {
		t.Fatal("heartbeat did not reach the cluster primitives")
	}

	if _, err := cp.Members(e.ctx); err != nil {
		t.Fatalf("Members: %v", err)
	}
	if _, err := cp.HealthyMembers(e.ctx); err != nil {
		t.Fatalf("HealthyMembers: %v", err)
	}
	if ok, err := cp.HasQuorum(e.ctx); err != nil || !ok {
		t.Fatalf("HasQuorum = %v, %v", ok, err)
	}
	if _, err := cp.AcquireLock(e.ctx, "nodes.sweep", time.Minute); err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	ran := false
	if err := cp.WithLock(e.ctx, "nodes.sweep", time.Minute, func(context.Context) error {
		ran = true
		return nil
	}); err != nil {
		t.Fatalf("WithLock: %v", err)
	}
	if !ran {
		t.Fatal("WithLock did not run the callback")
	}

	won, err := cp.ElectPrimary(e.ctx)
	if err != nil || !won {
		t.Fatalf("ElectPrimary = %v, %v", won, err)
	}
	if ok, err := cp.IsPrimary(e.ctx); err != nil || !ok {
		t.Fatalf("IsPrimary = %v, %v", ok, err)
	}
	if primary, err := cp.PrimaryID(e.ctx); err != nil || primary != node.ID {
		t.Fatalf("PrimaryID = %q, %v", primary, err)
	}

	id, err := e.svc.Authenticate(e.ctx, credential)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if _, err := e.svc.ControlPlaneFor(id); err != nil {
		t.Fatalf("ControlPlaneFor(cluster): %v", err)
	}
	if !ElectionEligible(node) {
		t.Fatal("an enrolled cluster node must be election eligible")
	}
}

func TestControlPlaneRefusesRemovedNode(t *testing.T) {
	e := newEnv(t)
	node, _ := e.enroll(t, RoleCluster, "cluster-a")

	if _, err := e.svc.Remove(e.ctx, RemoveRequest{NodeID: node.ID, Confirm: true, Actor: "admin"}); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := e.svc.ControlPlane(e.ctx, node.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("ControlPlane(removed) error = %v, want ErrInvalidTransition", err)
	}
}

func TestElectionEligibility(t *testing.T) {
	cases := []struct {
		name string
		node Node
		want bool
	}{
		{"enrolled cluster", Node{Role: RoleCluster, State: StateEnrolled}, true},
		{"online cluster", Node{Role: RoleCluster, State: StateOnline}, true},
		{"degraded cluster", Node{Role: RoleCluster, State: StateDegraded}, true},
		{"maintenance cluster", Node{Role: RoleCluster, State: StateOnline, Maintenance: true}, false},
		{"offline cluster", Node{Role: RoleCluster, State: StateOffline}, false},
		{"drained cluster", Node{Role: RoleCluster, State: StateDrained}, false},
		{"removed cluster", Node{Role: RoleCluster, State: StateRemoved}, false},
		{"online managed", Node{Role: RoleManaged, State: StateOnline}, false},
		{"enrolled managed", Node{Role: RoleManaged, State: StateEnrolled}, false},
	}
	for _, tc := range cases {
		if got := ElectionEligible(tc.node); got != tc.want {
			t.Fatalf("%s: ElectionEligible = %v, want %v", tc.name, got, tc.want)
		}
	}
}
