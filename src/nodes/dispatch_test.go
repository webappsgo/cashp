package nodes

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCoreActionsAreRegistered(t *testing.T) {
	e := newEnv(t)
	actions := e.svc.Actions()
	if len(actions) != len(coreActions) {
		t.Fatalf("registered %d actions, want %d", len(actions), len(coreActions))
	}
	for i := 1; i < len(actions); i++ {
		if actions[i-1].Name >= actions[i].Name {
			t.Fatalf("actions are not ordered by name: %v", actions)
		}
	}
	if _, err := e.svc.Action("agent.nope"); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("Action(unknown) error = %v", err)
	}
}

func TestRegisterActionValidatesAndIsIdempotent(t *testing.T) {
	e := newEnv(t)
	spec := ActionSpec{
		Name:        "hosting.deploy",
		Description: "Deploy a site",
		Roles:       []Role{RoleManaged},
		Timeout:     time.Minute,
		MaxAttempts: 2,
	}
	if err := e.svc.RegisterAction(spec); err != nil {
		t.Fatalf("RegisterAction: %v", err)
	}
	// Registering the identical spec again is a no-op, not a conflict.
	if err := e.svc.RegisterAction(spec); err != nil {
		t.Fatalf("second RegisterAction: %v", err)
	}
	conflicting := spec
	conflicting.MaxAttempts = 5
	if err := e.svc.RegisterAction(conflicting); !errors.Is(err, ErrNodeExists) {
		t.Fatalf("conflicting RegisterAction error = %v, want ErrNodeExists", err)
	}

	stored, err := e.svc.Action("hosting.deploy")
	if err != nil {
		t.Fatalf("Action: %v", err)
	}
	if stored.MaxPayloadBytes != DefaultMaxPayloadBytes || stored.MaxAttempts != 2 {
		t.Fatalf("stored spec = %+v", stored)
	}

	bad := []ActionSpec{
		{Name: ""},
		{Name: "nodots"},
		{Name: "too.many.dots"},
		{Name: "Bad.Case"},
		{Name: "shell.rm; rm -rf /"},
		{Name: strings.Repeat("a", MaxActionLen) + ".x"},
	}
	for _, spec := range bad {
		if err := e.svc.RegisterAction(spec); !errors.Is(err, ErrUnknownAction) {
			t.Fatalf("RegisterAction(%q) error = %v, want ErrUnknownAction", spec.Name, err)
		}
	}
	if err := e.svc.RegisterAction(ActionSpec{Name: "a.b", Roles: []Role{Role("root")}}); !errors.Is(err, ErrInvalidRole) {
		t.Fatal("RegisterAction accepted an unknown role")
	}
	if err := e.svc.RegisterAction(ActionSpec{Name: "a.b", MaxPayloadBytes: MaxPayloadBytesLimit + 1}); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatal("RegisterAction accepted an unbounded payload limit")
	}
}

func TestRegisterActionClampsExcessiveBudgets(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.RegisterAction(ActionSpec{
		Name:        "hosting.slow",
		Timeout:     100 * MaxTaskTimeout,
		MaxAttempts: MaxTaskAttempts * 10,
	}); err != nil {
		t.Fatalf("RegisterAction: %v", err)
	}
	spec, err := e.svc.Action("hosting.slow")
	if err != nil {
		t.Fatalf("Action: %v", err)
	}
	if spec.Timeout != MaxTaskTimeout || spec.MaxAttempts != MaxTaskAttempts {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestDispatchAuthorizesByRole(t *testing.T) {
	e := newEnv(t)
	managed, _ := e.enroll(t, RoleManaged, "managed-a")
	cluster, _ := e.enroll(t, RoleCluster, "cluster-a")

	if _, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: cluster.ID, Action: "agent.upgrade", Actor: "admin",
	}); !errors.Is(err, ErrActionNotAllowed) {
		t.Fatalf("cluster node accepted a managed-only action: %v", err)
	}
	if _, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: managed.ID, Action: "cluster.reload_config", Actor: "admin",
	}); !errors.Is(err, ErrActionNotAllowed) {
		t.Fatalf("managed node accepted a cluster-only action: %v", err)
	}
	if _, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: managed.ID, Action: "agent.made_up", Actor: "admin",
	}); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("unregistered action was dispatched: %v", err)
	}
	if _, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: "missing", Action: "agent.ping", Actor: "admin",
	}); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Dispatch to unknown node error = %v", err)
	}

	task, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: managed.ID, Action: "agent.upgrade", Actor: "admin",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if task.State != TaskQueued || task.NodeID != managed.ID {
		t.Fatalf("task = %+v", task)
	}
	if string(task.Payload) != "{}" {
		t.Fatalf("payload = %q, want an empty object", task.Payload)
	}
}

func TestDispatchPayloadIsBoundedAndObjectOnly(t *testing.T) {
	e := newEnv(t)
	node, _ := e.enroll(t, RoleManaged, "managed-a")

	hostile := []json.RawMessage{
		json.RawMessage(`[1,2,3]`),
		json.RawMessage(`"just a string"`),
		json.RawMessage(`7`),
		json.RawMessage(`null`),
		json.RawMessage(`{"a":1} trailing`),
		json.RawMessage(`{"a":1}{"b":2}`),
		json.RawMessage(`{not json}`),
	}
	for _, payload := range hostile {
		_, err := e.svc.Dispatch(e.ctx, DispatchRequest{
			NodeID: node.ID, Action: "agent.ping", Payload: payload, Actor: "admin",
		})
		if !errors.Is(err, ErrInvalidPayload) {
			t.Fatalf("Dispatch(%s) error = %v, want ErrInvalidPayload", payload, err)
		}
	}

	oversize := json.RawMessage(`{"k":"` + strings.Repeat("x", DefaultMaxPayloadBytes) + `"}`)
	if _, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: node.ID, Action: "agent.ping", Payload: oversize, Actor: "admin",
	}); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversize payload error = %v, want ErrPayloadTooLarge", err)
	}

	task, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID:  node.ID,
		Action:  "agent.ping",
		Payload: json.RawMessage("{\n  \"target\" : \"web\"\n}"),
		Actor:   "admin",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if string(task.Payload) != `{"target":"web"}` {
		t.Fatalf("payload = %q, want the compacted form", task.Payload)
	}
}

func TestClaimIsScopedToTheAuthenticatedNode(t *testing.T) {
	e := newEnv(t)
	first, credential := e.enroll(t, RoleManaged, "managed-a")
	second, _ := e.enroll(t, RoleManaged, "managed-b")

	mine, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: first.ID, Action: "agent.collect_facts", Actor: "admin",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	theirs, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: second.ID, Action: "agent.collect_facts", Actor: "admin",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	id, err := e.svc.Authenticate(e.ctx, credential)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	claimed, err := e.svc.Claim(e.ctx, id, 0)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != mine.ID {
		t.Fatalf("claimed = %+v, want only this node's task", claimed)
	}
	if claimed[0].State != TaskDispatched || claimed[0].Attempts != 1 {
		t.Fatalf("claimed task = %+v", claimed[0])
	}
	if !claimed[0].DeadlineAt.Equal(e.clock.now().Add(claimed[0].Timeout)) {
		t.Fatal("claim did not set the attempt deadline")
	}

	// A second poll finds nothing: the task is no longer queued.
	again, err := e.svc.Claim(e.ctx, id, 0)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("re-claimed %d tasks", len(again))
	}

	// Reporting another node's task is a not-found, never a cross-node write.
	if _, err := e.svc.Report(e.ctx, id, TaskResult{TaskID: theirs.ID, Success: true}); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("cross-node Report error = %v, want ErrTaskNotFound", err)
	}
}

func TestClaimRefusesDrainedNode(t *testing.T) {
	e := newEnv(t)
	node, credential := e.enroll(t, RoleManaged, "managed-a")
	id, err := e.svc.Authenticate(e.ctx, credential)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if _, err := e.svc.Drain(e.ctx, node.ID, "maintenance", "admin"); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	id.Node, err = e.svc.Get(e.ctx, node.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := e.svc.Claim(e.ctx, id, 0); !errors.Is(err, ErrNodeNotSchedulable) {
		t.Fatalf("Claim on a drained node error = %v", err)
	}
}

func TestReportSuccessBoundsHostileStrings(t *testing.T) {
	e := newEnv(t)
	node, credential := e.enroll(t, RoleManaged, "managed-a")
	if _, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: node.ID, Action: "agent.collect_facts", Actor: "admin",
	}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	id, err := e.svc.Authenticate(e.ctx, credential)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	claimed, err := e.svc.Claim(e.ctx, id, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim = %v, %v", claimed, err)
	}

	done, err := e.svc.Report(e.ctx, id, TaskResult{
		TaskID:   claimed[0].ID,
		Success:  true,
		ExitCode: 0,
		Result:   strings.Repeat("A", MaxResultLen*2) + "\x00\x07",
	})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if done.State != TaskSucceeded {
		t.Fatalf("state = %q", done.State)
	}
	if len(done.Result) > MaxResultLen {
		t.Fatalf("result kept %d bytes, want <= %d", len(done.Result), MaxResultLen)
	}
	if strings.ContainsAny(done.Result, "\x00\x07") {
		t.Fatal("control characters survived into storage")
	}

	// Reporting the same task again is refused: it is no longer claimed.
	if _, err := e.svc.Report(e.ctx, id, TaskResult{TaskID: claimed[0].ID, Success: true}); !errors.Is(err, ErrTaskNotClaimed) {
		t.Fatalf("second Report error = %v, want ErrTaskNotClaimed", err)
	}
}

func TestReportFailureRetriesThenFails(t *testing.T) {
	e := newEnv(t)
	node, credential := e.enroll(t, RoleManaged, "managed-a")
	dispatched, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: node.ID, Action: "agent.collect_facts", MaxAttempts: 2, Actor: "admin",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if dispatched.MaxAttempts != 2 {
		t.Fatalf("max attempts = %d", dispatched.MaxAttempts)
	}
	id, err := e.svc.Authenticate(e.ctx, credential)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	claimed, err := e.svc.Claim(e.ctx, id, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim = %v, %v", claimed, err)
	}
	requeued, err := e.svc.Report(e.ctx, id, TaskResult{TaskID: claimed[0].ID, Error: "backend refused"})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if requeued.State != TaskQueued {
		t.Fatalf("state = %q, want queued", requeued.State)
	}
	if !requeued.NextAttemptAt.Equal(e.clock.now().Add(RetryDelay(1))) {
		t.Fatalf("next attempt = %v, want a %v backoff", requeued.NextAttemptAt, RetryDelay(1))
	}

	// The backoff is respected: the node cannot immediately re-claim.
	if again, err := e.svc.Claim(e.ctx, id, 1); err != nil || len(again) != 0 {
		t.Fatalf("claimed during backoff: %v, %v", again, err)
	}

	e.clock.advance(RetryDelay(1))
	second, err := e.svc.Claim(e.ctx, id, 1)
	if err != nil || len(second) != 1 {
		t.Fatalf("Claim after backoff = %v, %v", second, err)
	}
	if second[0].Attempts != 2 {
		t.Fatalf("attempts = %d", second[0].Attempts)
	}

	failed, err := e.svc.Report(e.ctx, id, TaskResult{TaskID: second[0].ID, Error: "backend refused"})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if failed.State != TaskFailed {
		t.Fatalf("state = %q, want failed after the budget is spent", failed.State)
	}
	if failed.Error != "backend refused" {
		t.Fatalf("error = %q", failed.Error)
	}
}

func TestReportDefaultsMissingFailureText(t *testing.T) {
	e := newEnv(t)
	node, credential := e.enroll(t, RoleManaged, "managed-a")
	if _, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: node.ID, Action: "agent.ping", Actor: "admin",
	}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	id, err := e.svc.Authenticate(e.ctx, credential)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	claimed, err := e.svc.Claim(e.ctx, id, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim = %v, %v", claimed, err)
	}
	// agent.ping has a one-attempt budget, so a bare failure is terminal.
	failed, err := e.svc.Report(e.ctx, id, TaskResult{TaskID: claimed[0].ID})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if failed.State != TaskFailed || failed.Error != "node reported failure" {
		t.Fatalf("task = %+v", failed)
	}
}

func TestRetryDelayBackoff(t *testing.T) {
	cases := []struct {
		attempts int64
		want     time.Duration
	}{
		{-5, RetryBaseDelay},
		{0, RetryBaseDelay},
		{1, RetryBaseDelay},
		{2, 2 * RetryBaseDelay},
		{3, 4 * RetryBaseDelay},
		{100, RetryMaxDelay},
	}
	for _, tc := range cases {
		if got := RetryDelay(tc.attempts); got != tc.want {
			t.Fatalf("RetryDelay(%d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
	if RetryDelay(64) > RetryMaxDelay {
		t.Fatal("backoff exceeded its cap")
	}
}

func TestReapTasksHandlesTimeouts(t *testing.T) {
	e := newEnv(t)
	node, credential := e.enroll(t, RoleManaged, "managed-a")
	if _, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: node.ID, Action: "agent.collect_facts", MaxAttempts: 2, Actor: "admin",
	}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	id, err := e.svc.Authenticate(e.ctx, credential)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	claimed, err := e.svc.Claim(e.ctx, id, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim = %v, %v", claimed, err)
	}

	// Before the deadline the reaper leaves the task alone.
	if reaped, err := e.svc.ReapTasks(e.ctx); err != nil || reaped != 0 {
		t.Fatalf("premature reap: %d, %v", reaped, err)
	}

	e.clock.advance(claimed[0].Timeout + time.Second)
	reaped, err := e.svc.ReapTasks(e.ctx)
	if err != nil {
		t.Fatalf("ReapTasks: %v", err)
	}
	if reaped != 1 {
		t.Fatalf("reaped %d tasks, want 1", reaped)
	}
	stored, err := e.svc.Task(e.ctx, claimed[0].ID)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if stored.State != TaskQueued || stored.Error != "attempt timed out" {
		t.Fatalf("task = %+v", stored)
	}

	// The second attempt times out with no budget left, so it fails for good.
	e.clock.advance(RetryDelay(1))
	second, err := e.svc.Claim(e.ctx, id, 1)
	if err != nil || len(second) != 1 {
		t.Fatalf("Claim = %v, %v", second, err)
	}
	e.clock.advance(second[0].Timeout + time.Second)
	if reaped, err := e.svc.ReapTasks(e.ctx); err != nil || reaped != 1 {
		t.Fatalf("ReapTasks = %d, %v", reaped, err)
	}
	stored, err = e.svc.Task(e.ctx, claimed[0].ID)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if stored.State != TaskFailed {
		t.Fatalf("state = %q, want failed", stored.State)
	}
}

func TestCancelTask(t *testing.T) {
	e := newEnv(t)
	node, _ := e.enroll(t, RoleManaged, "managed-a")
	task, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: node.ID, Action: "agent.ping", Actor: "admin",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	cancelled, err := e.svc.CancelTask(e.ctx, task.ID, "", "admin")
	if err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	if cancelled.State != TaskCancelled || cancelled.Error != "cancelled by operator" {
		t.Fatalf("task = %+v", cancelled)
	}
	if !cancelled.Terminal() {
		t.Fatal("a cancelled task must be terminal")
	}
	if _, err := e.svc.CancelTask(e.ctx, task.ID, "", "admin"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second CancelTask error = %v, want ErrInvalidTransition", err)
	}
	if _, err := e.svc.CancelTask(e.ctx, "no-such-task", "", "admin"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("CancelTask(unknown) error = %v", err)
	}
}

func TestListTasks(t *testing.T) {
	e := newEnv(t)
	node, _ := e.enroll(t, RoleManaged, "managed-a")
	for i := 0; i < 3; i++ {
		if _, err := e.svc.Dispatch(e.ctx, DispatchRequest{
			NodeID: node.ID, Action: "agent.collect_facts", Actor: "admin",
		}); err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
	}

	all, err := e.svc.ListTasks(e.ctx, node.ID, "", 0)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("listed %d tasks, want 3", len(all))
	}
	queued, err := e.svc.ListTasks(e.ctx, node.ID, TaskQueued, 2)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(queued) != 2 {
		t.Fatalf("listed %d queued tasks, want the limit of 2", len(queued))
	}
	if _, err := e.svc.ListTasks(e.ctx, "Bad ID!", "", 0); !errors.Is(err, ErrInvalidNodeID) {
		t.Fatalf("ListTasks(hostile id) error = %v", err)
	}
}
