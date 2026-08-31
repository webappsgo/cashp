package nodes

import (
	"errors"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/scheduler"
)

func TestStateMachineTransitions(t *testing.T) {
	valid := []struct{ from, to State }{
		{StatePending, StateEnrolled},
		{StatePending, StateRemoved},
		{StateEnrolled, StateOnline},
		{StateEnrolled, StateDrained},
		{StateOnline, StateDegraded},
		{StateDegraded, StateOffline},
		{StateOffline, StateOnline},
		{StateOffline, StateEnrolled},
		{StateDrained, StateEnrolled},
		{StateDrained, StateRemoved},
	}
	for _, tc := range valid {
		if !CanTransition(tc.from, tc.to) {
			t.Fatalf("CanTransition(%s, %s) = false, want true", tc.from, tc.to)
		}
	}

	invalid := []struct{ from, to State }{
		{StatePending, StateOnline},
		{StatePending, StateDrained},
		{StatePending, StateDegraded},
		{StateRemoved, StateEnrolled},
		{StateRemoved, StateOnline},
		{StateRemoved, StateRemoved},
		{StateDrained, StateDegraded},
		{StateDrained, StateOffline},
		{State("bogus"), StateOnline},
		{StateOnline, State("bogus")},
	}
	for _, tc := range invalid {
		if CanTransition(tc.from, tc.to) {
			t.Fatalf("CanTransition(%s, %s) = true, want false", tc.from, tc.to)
		}
	}

	if !StateOnline.Valid() || State("bogus").Valid() {
		t.Fatal("State.Valid is wrong")
	}
}

func TestTransitionRejectsInvalidTargetAndRemoval(t *testing.T) {
	e := newEnv(t)
	node, _ := e.enroll(t, RoleManaged, "node-a")

	if _, err := e.svc.Transition(e.ctx, node.ID, State("bogus"), "", "admin"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Transition(bogus) error = %v", err)
	}
	if _, err := e.svc.Transition(e.ctx, node.ID, StateRemoved, "", "admin"); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("Transition(removed) error = %v, want ErrConfirmationRequired", err)
	}
	if _, err := e.svc.Transition(e.ctx, node.ID, StatePending, "", "admin"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Transition(pending) error = %v, want ErrInvalidTransition", err)
	}

	moved, err := e.svc.Transition(e.ctx, node.ID, StateOnline, "manual", "admin")
	if err != nil {
		t.Fatalf("Transition(online): %v", err)
	}
	if moved.State != StateOnline || moved.StateReason != "manual" {
		t.Fatalf("node = %+v", moved)
	}
	if moved.Version <= node.Version {
		t.Fatal("transition must bump the row version")
	}
}

func TestRecordContactBringsNodeOnline(t *testing.T) {
	e := newEnv(t)
	node, credential := e.enroll(t, RoleManaged, "node-a")
	if node.State != StateEnrolled {
		t.Fatalf("state after enroll = %q", node.State)
	}

	id, err := e.svc.Authenticate(e.ctx, credential)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	e.clock.advance(time.Minute)

	updated, err := e.svc.RecordContact(e.ctx, id, "1.1.0")
	if err != nil {
		t.Fatalf("RecordContact: %v", err)
	}
	if updated.State != StateOnline {
		t.Fatalf("state = %q, want online", updated.State)
	}
	if updated.AgentVersion != "1.1.0" {
		t.Fatalf("agent version = %q", updated.AgentVersion)
	}
	if LastContact(updated, e.clock.now()) != 0 {
		t.Fatal("last contact should be now")
	}

	// A hostile version string never reaches storage.
	if _, err := e.svc.RecordContact(e.ctx, id, "1.0\x00bad"); !errors.Is(err, ErrInvalidFacts) {
		t.Fatalf("RecordContact(hostile version) error = %v", err)
	}
}

func TestRecordContactRespectsMaintenance(t *testing.T) {
	e := newEnv(t)
	node, credential := e.enroll(t, RoleManaged, "node-a")

	if _, err := e.svc.SetMaintenance(e.ctx, node.ID, true, "planned reboot", "admin"); err != nil {
		t.Fatalf("SetMaintenance: %v", err)
	}
	id, err := e.svc.Authenticate(e.ctx, credential)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	updated, err := e.svc.RecordContact(e.ctx, id, "")
	if err != nil {
		t.Fatalf("RecordContact: %v", err)
	}
	if updated.State != StateEnrolled {
		t.Fatalf("maintenance node moved to %q", updated.State)
	}
	if updated.Schedulable() {
		t.Fatal("a node in maintenance must not be schedulable")
	}

	cleared, err := e.svc.SetMaintenance(e.ctx, node.ID, false, "", "admin")
	if err != nil {
		t.Fatalf("SetMaintenance(false): %v", err)
	}
	if cleared.Maintenance || !cleared.Schedulable() {
		t.Fatalf("node = %+v", cleared)
	}
}

func TestCordonAndUncordon(t *testing.T) {
	e := newEnv(t)
	node, _ := e.enroll(t, RoleManaged, "node-a")

	cordoned, err := e.svc.Cordon(e.ctx, node.ID, "investigating", "admin")
	if err != nil {
		t.Fatalf("Cordon: %v", err)
	}
	if !cordoned.Cordoned || cordoned.Schedulable() {
		t.Fatalf("node = %+v", cordoned)
	}
	if cordoned.State != StateEnrolled {
		t.Fatalf("cordon changed state to %q", cordoned.State)
	}
	if _, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: node.ID, Action: "agent.ping", Actor: "admin",
	}); !errors.Is(err, ErrNodeNotSchedulable) {
		t.Fatalf("Dispatch to cordoned node error = %v", err)
	}

	uncordoned, err := e.svc.Uncordon(e.ctx, node.ID, "admin")
	if err != nil {
		t.Fatalf("Uncordon: %v", err)
	}
	if uncordoned.Cordoned || !uncordoned.Schedulable() {
		t.Fatalf("node = %+v", uncordoned)
	}
}

func TestDrainCancelsQueuedWork(t *testing.T) {
	e := newEnv(t)
	node, _ := e.enroll(t, RoleManaged, "node-a")

	task, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: node.ID, Action: "agent.ping", Actor: "admin",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	result, err := e.svc.Drain(e.ctx, node.ID, "decommissioning", "admin")
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if result.Node.State != StateDrained || !result.Node.Cordoned {
		t.Fatalf("node = %+v", result.Node)
	}
	if result.CancelledTasks != 1 {
		t.Fatalf("cancelled %d tasks, want 1", result.CancelledTasks)
	}

	stored, err := e.svc.Task(e.ctx, task.ID)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if stored.State != TaskCancelled {
		t.Fatalf("task state = %q", stored.State)
	}
	if _, err := e.svc.Drain(e.ctx, node.ID, "again", "admin"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second Drain error = %v", err)
	}
}

func TestRemoveRequiresConfirmation(t *testing.T) {
	e := newEnv(t)
	node, credential := e.enroll(t, RoleManaged, "node-a")

	if _, err := e.svc.Remove(e.ctx, RemoveRequest{NodeID: node.ID, Actor: "admin"}); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("Remove without confirmation error = %v, want ErrConfirmationRequired", err)
	}
	if _, err := e.svc.Authenticate(e.ctx, credential); err != nil {
		t.Fatalf("credential must survive an unconfirmed removal: %v", err)
	}

	removed, err := e.svc.Remove(e.ctx, RemoveRequest{
		NodeID: node.ID, Confirm: true, Reason: "returned to vendor", Actor: "admin",
	})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed.State != StateRemoved {
		t.Fatalf("state = %q", removed.State)
	}
	if _, err := e.svc.Authenticate(e.ctx, credential); !errors.Is(err, ErrCredentialRejected) {
		t.Fatalf("removed node still authenticates: %v", err)
	}
	// A managed node never had a control-plane membership row to drop.
	if e.cluster.callCount("RemoveNode") != 0 {
		t.Fatal("removing a managed node touched the control plane")
	}
	if _, err := e.svc.Remove(e.ctx, RemoveRequest{NodeID: node.ID, Confirm: true, Actor: "admin"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second Remove error = %v", err)
	}
}

func TestRemoveClusterNodeDropsMembership(t *testing.T) {
	e := newEnv(t)
	node, _ := e.enroll(t, RoleCluster, "cluster-a")

	if _, err := e.svc.Remove(e.ctx, RemoveRequest{NodeID: node.ID, Confirm: true, Actor: "admin"}); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if e.cluster.callCount("RemoveNode") != 1 {
		t.Fatal("removing a cluster node must drop its control-plane membership")
	}
	if len(e.cluster.removed) != 1 || e.cluster.removed[0] != node.ID {
		t.Fatalf("removed = %v", e.cluster.removed)
	}
}

func TestSweepLiveness(t *testing.T) {
	e := newEnv(t)
	node, _ := e.enroll(t, RoleManaged, "node-a")
	quiet, _ := e.enroll(t, RoleManaged, "node-b")

	if _, err := e.svc.SetMaintenance(e.ctx, quiet.ID, true, "planned", "admin"); err != nil {
		t.Fatalf("SetMaintenance: %v", err)
	}

	e.clock.advance(database.DegradedAfter)
	result, err := e.svc.SweepLiveness(e.ctx)
	if err != nil {
		t.Fatalf("SweepLiveness: %v", err)
	}
	if result.Degraded != 1 || result.Offline != 0 {
		t.Fatalf("sweep = %+v", result)
	}
	got, err := e.svc.Get(e.ctx, node.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateDegraded {
		t.Fatalf("state = %q, want degraded", got.State)
	}

	e.clock.advance(database.OfflineAfter)
	result, err = e.svc.SweepLiveness(e.ctx)
	if err != nil {
		t.Fatalf("SweepLiveness: %v", err)
	}
	if result.Offline != 1 {
		t.Fatalf("sweep = %+v", result)
	}
	got, err = e.svc.Get(e.ctx, node.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateOffline {
		t.Fatalf("state = %q, want offline", got.State)
	}

	// The node in maintenance was left alone throughout.
	held, err := e.svc.Get(e.ctx, quiet.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if held.State != StateEnrolled {
		t.Fatalf("maintenance node state = %q", held.State)
	}
}

func TestSchedulerTasksAreClusterWide(t *testing.T) {
	e := newEnv(t)
	tasks := e.svc.SchedulerTasks()
	if len(tasks) != 3 {
		t.Fatalf("registered %d tasks, want 3", len(tasks))
	}
	seen := map[string]bool{}
	for _, task := range tasks {
		if !task.ClusterWide {
			t.Fatalf("task %q is not cluster-wide", task.Name)
		}
		if task.Run == nil || task.Schedule == "" {
			t.Fatalf("task %q is incomplete", task.Name)
		}
		if err := task.Run(e.ctx); err != nil {
			t.Fatalf("task %q run: %v", task.Name, err)
		}
		seen[task.Name] = true
	}
	for _, name := range []string{TaskLivenessSweep, TaskReaper, TaskTokenExpiry} {
		if !seen[name] {
			t.Fatalf("task %q was not registered", name)
		}
	}
}

// fakeRegistrar captures scheduler registrations without starting one.
type fakeRegistrar struct {
	names []string
}

// Register implements TaskRegistrar.
func (f *fakeRegistrar) Register(t scheduler.Task) error {
	f.names = append(f.names, t.Name)
	return nil
}

func TestRegisterTasks(t *testing.T) {
	e := newEnv(t)
	reg := &fakeRegistrar{}
	if err := e.svc.RegisterTasks(reg); err != nil {
		t.Fatalf("RegisterTasks: %v", err)
	}
	if len(reg.names) != 3 {
		t.Fatalf("registered %v", reg.names)
	}
}
