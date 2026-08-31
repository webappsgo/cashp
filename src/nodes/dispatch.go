package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

// TaskState is the lifecycle of one dispatched unit of work.
type TaskState string

// Task states. A task is queued when dispatched, dispatched once a node has
// claimed it, and then terminal: succeeded, failed or cancelled. A failed
// attempt that still has budget returns to queued with a backoff delay.
const (
	// TaskQueued is work waiting for its node to claim it.
	TaskQueued TaskState = "queued"
	// TaskDispatched is work a node currently holds.
	TaskDispatched TaskState = "dispatched"
	// TaskSucceeded is work the node reported as complete.
	TaskSucceeded TaskState = "succeeded"
	// TaskFailed is work that exhausted its attempt budget.
	TaskFailed TaskState = "failed"
	// TaskCancelled is work an operator, a drain or a removal withdrew.
	TaskCancelled TaskState = "cancelled"
)

// ActionSpec declares a dispatchable action and the policy that governs it.
// An action that is not registered can never be dispatched, so the action
// namespace is a closed allowlist rather than free-form text a caller
// supplies.
type ActionSpec struct {
	// Name is the "subsystem.verb" action identifier.
	Name string
	// Description is the operator-facing summary.
	Description string
	// Roles lists the node roles allowed to run the action. An empty list
	// means both roles. This is the authorization rule enforced on every
	// dispatch.
	Roles []Role
	// MaxPayloadBytes bounds the JSON payload. Zero uses
	// DefaultMaxPayloadBytes.
	MaxPayloadBytes int
	// Timeout is how long a claimed attempt may run. Zero uses
	// DefaultTaskTimeout.
	Timeout time.Duration
	// MaxAttempts is the delivery budget. Zero uses DefaultMaxAttempts.
	MaxAttempts int
}

// DefaultMaxPayloadBytes bounds a dispatch payload when the action does not
// set its own limit.
const DefaultMaxPayloadBytes = 16 << 10

// MaxPayloadBytesLimit is the hard ceiling an action may raise its payload
// bound to.
const MaxPayloadBytesLimit = 256 << 10

// MaxClaimBatch bounds how many tasks a node may claim in one poll.
const MaxClaimBatch = 32

// allows reports whether a role may run the action.
func (a ActionSpec) allows(role Role) bool {
	if len(a.Roles) == 0 {
		return true
	}
	for _, r := range a.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// coreActions are the actions this package owns. Everything else is
// registered by the subsystem that implements it, so no package has to
// widen this list to add work of its own.
var coreActions = []ActionSpec{
	{
		Name:        "agent.ping",
		Description: "Liveness probe answered by the node",
		Timeout:     30 * time.Second,
		MaxAttempts: 1,
	},
	{
		Name:        "agent.collect_facts",
		Description: "Ask the node to re-report its capability inventory",
		Timeout:     2 * time.Minute,
	},
	{
		Name:        "agent.upgrade",
		Description: "Ask a managed node to upgrade its agent binary",
		Roles:       []Role{RoleManaged},
		Timeout:     10 * time.Minute,
	},
	{
		Name:        "cluster.reload_config",
		Description: "Ask a cluster node to reload its configuration",
		Roles:       []Role{RoleCluster},
		Timeout:     time.Minute,
	},
}

// RegisterAction adds a dispatchable action. Registering the same name twice
// with a different definition is a conflict rather than a silent overwrite,
// so two subsystems cannot quietly fight over one action.
func (s *Service) RegisterAction(spec ActionSpec) error {
	if err := ValidateActionName(spec.Name); err != nil {
		return err
	}
	for _, role := range spec.Roles {
		if !role.Valid() {
			return ErrInvalidRole
		}
	}
	if spec.MaxPayloadBytes < 0 || spec.MaxPayloadBytes > MaxPayloadBytesLimit {
		return ErrPayloadTooLarge
	}
	if spec.MaxPayloadBytes == 0 {
		spec.MaxPayloadBytes = DefaultMaxPayloadBytes
	}
	if spec.Timeout <= 0 {
		spec.Timeout = DefaultTaskTimeout
	}
	if spec.Timeout > MaxTaskTimeout {
		spec.Timeout = MaxTaskTimeout
	}
	if spec.MaxAttempts <= 0 {
		spec.MaxAttempts = DefaultMaxAttempts
	}
	if spec.MaxAttempts > MaxTaskAttempts {
		spec.MaxAttempts = MaxTaskAttempts
	}
	spec.Description = truncate(spec.Description, MaxReasonLen)

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.actions[spec.Name]; ok && !sameAction(existing, spec) {
		return ErrNodeExists
	}
	s.actions[spec.Name] = spec
	return nil
}

// sameAction reports whether two specs are identical, which makes repeated
// registration of the same action idempotent.
func sameAction(a, b ActionSpec) bool {
	if a.Name != b.Name || a.Description != b.Description ||
		a.MaxPayloadBytes != b.MaxPayloadBytes || a.Timeout != b.Timeout ||
		a.MaxAttempts != b.MaxAttempts || len(a.Roles) != len(b.Roles) {
		return false
	}
	for i := range a.Roles {
		if a.Roles[i] != b.Roles[i] {
			return false
		}
	}
	return true
}

// Action returns a registered action definition.
func (s *Service) Action(name string) (ActionSpec, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	spec, ok := s.actions[name]
	if !ok {
		return ActionSpec{}, ErrUnknownAction
	}
	return spec, nil
}

// Actions returns every registered action, ordered by name.
func (s *Service) Actions() []ActionSpec {
	s.mu.RLock()
	names := make([]string, 0, len(s.actions))
	for name := range s.actions {
		names = append(names, name)
	}
	specs := make(map[string]ActionSpec, len(s.actions))
	for name, spec := range s.actions {
		specs[name] = spec
	}
	s.mu.RUnlock()

	sortStrings(names)
	out := make([]ActionSpec, 0, len(names))
	for _, name := range names {
		out = append(out, specs[name])
	}
	return out
}

// Task is one unit of work assigned to a node.
type Task struct {
	// ID is the task identifier.
	ID string
	// NodeID is the node the task is assigned to.
	NodeID string
	// Action is the registered action name.
	Action string
	// Payload is the validated JSON object handed to the node verbatim.
	Payload json.RawMessage
	// State is the task lifecycle state.
	State TaskState
	// Attempts is how many times the task has been claimed.
	Attempts int64
	// MaxAttempts is the delivery budget.
	MaxAttempts int64
	// Timeout is how long one claimed attempt may run.
	Timeout time.Duration
	// CreatedBy is the actor that dispatched the task.
	CreatedBy string
	// CreatedAt is when the task was dispatched.
	CreatedAt time.Time
	// NextAttemptAt is when the task becomes claimable again.
	NextAttemptAt time.Time
	// ClaimedAt is when the node last claimed the task.
	ClaimedAt time.Time
	// DeadlineAt is when the current attempt times out.
	DeadlineAt time.Time
	// FinishedAt is when the task reached a terminal state.
	FinishedAt time.Time
	// ExitCode is the node-reported exit status.
	ExitCode int64
	// Result is the node-reported result, size-bounded on report.
	Result string
	// Error is the node-reported or reaper-recorded failure, size-bounded.
	Error string
	// Version is the optimistic-locking row version.
	Version int64
}

// Terminal reports whether the task has reached a final state.
func (t Task) Terminal() bool {
	switch t.State {
	case TaskSucceeded, TaskFailed, TaskCancelled:
		return true
	default:
		return false
	}
}

// taskColumns is the fixed column list every task read uses.
const taskColumns = `id, node_id, action, payload, state, attempts, max_attempts,
	timeout_seconds, created_by, created_at, next_attempt_at, claimed_at,
	deadline_at, finished_at, exit_code, result, error, version`

// scanTaskRow reads one node_tasks row.
func scanTaskRow(sc rowScanner) (Task, error) {
	var (
		task     Task
		payload  string
		state    string
		timeout  int64
		created  int64
		next     int64
		claimed  int64
		deadline int64
		finished int64
	)
	err := sc.Scan(&task.ID, &task.NodeID, &task.Action, &payload, &state,
		&task.Attempts, &task.MaxAttempts, &timeout, &task.CreatedBy, &created,
		&next, &claimed, &deadline, &finished, &task.ExitCode, &task.Result,
		&task.Error, &task.Version)
	if err != nil {
		return Task{}, err
	}
	task.Payload = json.RawMessage(payload)
	task.State = TaskState(state)
	task.Timeout = time.Duration(timeout) * time.Second
	task.CreatedAt = unixOrZero(created)
	task.NextAttemptAt = unixOrZero(next)
	task.ClaimedAt = unixOrZero(claimed)
	task.DeadlineAt = unixOrZero(deadline)
	task.FinishedAt = unixOrZero(finished)
	return task, nil
}

// DispatchRequest asks the control plane to assign work to a node.
type DispatchRequest struct {
	// NodeID is the target node. Required.
	NodeID string
	// Action is a registered action name. Required.
	Action string
	// Payload is a JSON object. Empty dispatches an empty object.
	Payload json.RawMessage
	// Timeout overrides the action's per-attempt timeout when non-zero.
	Timeout time.Duration
	// MaxAttempts overrides the action's delivery budget when non-zero.
	MaxAttempts int
	// Actor is the administrator or subsystem dispatching the work.
	Actor string
	// Notify sends a best-effort wake-up push to the node's callback URL
	// after the task is stored. A push that fails never fails the dispatch:
	// the node picks the task up on its next poll regardless.
	Notify bool
}

// Dispatch assigns work to a node. It authorizes the action against the
// node's role, validates and bounds the payload, and records the task so the
// node can claim it on its next poll.
func (s *Service) Dispatch(ctx context.Context, req DispatchRequest) (Task, error) {
	node, err := s.Get(ctx, req.NodeID)
	if err != nil {
		return Task{}, err
	}
	spec, err := s.Action(req.Action)
	if err != nil {
		return Task{}, err
	}
	if !spec.allows(node.Role) {
		return Task{}, ErrActionNotAllowed
	}
	if !node.Schedulable() {
		return Task{}, ErrNodeNotSchedulable
	}

	payload, err := normalizePayload(req.Payload, spec.MaxPayloadBytes)
	if err != nil {
		return Task{}, err
	}

	timeout := spec.Timeout
	if req.Timeout > 0 {
		timeout = req.Timeout
	}
	if timeout > MaxTaskTimeout {
		timeout = MaxTaskTimeout
	}
	attempts := int64(spec.MaxAttempts)
	if req.MaxAttempts > 0 {
		attempts = int64(req.MaxAttempts)
	}
	if attempts > MaxTaskAttempts {
		attempts = MaxTaskAttempts
	}

	id, err := newID()
	if err != nil {
		return Task{}, wrapInternal(err, "generate task id")
	}
	now := s.now()
	task := Task{
		ID:            id,
		NodeID:        node.ID,
		Action:        spec.Name,
		Payload:       payload,
		State:         TaskQueued,
		MaxAttempts:   attempts,
		Timeout:       timeout,
		CreatedBy:     truncate(req.Actor, MaxNodeNameLen),
		CreatedAt:     now,
		NextAttemptAt: now,
		Version:       1,
	}

	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO node_tasks (`+taskColumns+`)
		 VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, 0, 0, 0, 0, '', '', 1)`,
		task.ID, task.NodeID, task.Action, string(task.Payload), string(task.State),
		task.MaxAttempts, int64(timeout/time.Second), task.CreatedBy,
		unixOf(task.CreatedAt), unixOf(task.NextAttemptAt)); err != nil {
		return Task{}, wrapInternal(err, "store node task")
	}

	s.audit("nodes.task_dispatched", task.CreatedBy, node.ID,
		"task_id", task.ID, "action", task.Action, "role", string(node.Role),
		"max_attempts", task.MaxAttempts, "timeout_seconds", int64(timeout/time.Second))

	if req.Notify {
		if err := s.Notify(ctx, node); err != nil {
			s.audit("nodes.notify_failed", task.CreatedBy, node.ID, "task_id", task.ID)
		}
	}
	return task, nil
}

// normalizePayload validates that a dispatch payload is a bounded JSON
// object and returns its compact form. Anything else is rejected, so no
// caller can smuggle a scalar, an array or trailing bytes into storage.
func normalizePayload(raw json.RawMessage, limit int) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if limit <= 0 {
		limit = DefaultMaxPayloadBytes
	}
	if len(raw) > limit {
		return nil, ErrPayloadTooLarge
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var probe map[string]json.RawMessage
	if err := dec.Decode(&probe); err != nil {
		return nil, ErrInvalidPayload
	}
	// A literal null decodes into a nil map without error, so it is rejected
	// explicitly rather than stored as a payload no node can act on.
	if probe == nil {
		return nil, ErrInvalidPayload
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, ErrInvalidPayload
	}

	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, ErrInvalidPayload
	}
	if buf.Len() > limit {
		return nil, ErrPayloadTooLarge
	}
	return json.RawMessage(buf.Bytes()), nil
}

// Claim hands a node the work waiting for it. The node's identity comes from
// its authenticated credential, never from a request field, so a node can
// only ever claim its own tasks. Claiming is the pull half of dispatch: the
// control plane never needs an inbound route to the node for work to flow.
func (s *Service) Claim(ctx context.Context, id Identity, limit int) ([]Task, error) {
	if limit <= 0 || limit > MaxClaimBatch {
		limit = MaxClaimBatch
	}
	node := id.Node
	if node.State == StateRemoved || node.State == StateDrained {
		return nil, ErrNodeNotSchedulable
	}

	now := s.now()
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+taskColumns+` FROM node_tasks
		 WHERE node_id = ? AND state = ? AND next_attempt_at <= ?
		 ORDER BY created_at, id LIMIT ?`,
		node.ID, string(TaskQueued), unixOf(now), limit)
	if err != nil {
		return nil, wrapInternal(err, "list claimable tasks")
	}

	var candidates []Task
	for rows.Next() {
		task, err := scanTaskRow(rows)
		if err != nil {
			_ = rows.Close()
			return nil, wrapInternal(err, "scan node task")
		}
		candidates = append(candidates, task)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, wrapInternal(err, "list claimable tasks")
	}
	if err := rows.Close(); err != nil {
		return nil, wrapInternal(err, "list claimable tasks")
	}

	claimed := make([]Task, 0, len(candidates))
	for _, task := range candidates {
		deadline := now.Add(task.Timeout)
		err := s.db.UpdateVersioned(ctx,
			`UPDATE node_tasks SET state = ?, attempts = attempts + 1, claimed_at = ?,
				deadline_at = ?, version = version + 1
			 WHERE id = ? AND version = ? AND state = ?`,
			string(TaskDispatched), unixOf(now), unixOf(deadline), task.ID,
			task.Version, string(TaskQueued))
		if database.IsConflict(err) {
			// Another poll from the same node won the row; skip it.
			continue
		}
		if err != nil {
			return nil, wrapInternal(err, "claim node task")
		}

		task.State = TaskDispatched
		task.Attempts++
		task.ClaimedAt = now
		task.DeadlineAt = deadline
		task.Version++
		claimed = append(claimed, task)

		s.audit("nodes.task_claimed", "node", node.ID,
			"task_id", task.ID, "action", task.Action, "attempt", task.Attempts)
	}
	return claimed, nil
}

// TaskResult is what a node reports back for a claimed task. Every field is
// untrusted input: the strings are stripped of control characters and
// truncated before storage and never interpolated anywhere.
type TaskResult struct {
	// TaskID is the task being reported. Required.
	TaskID string
	// Success is whether the action completed.
	Success bool
	// ExitCode is the process exit status, if any.
	ExitCode int64
	// Result is a short node-supplied result summary.
	Result string
	// Error is a short node-supplied failure description.
	Error string
}

// Report records a node's outcome for a task it currently holds. A failed
// attempt with budget left returns to the queue with an exponential backoff;
// otherwise the task fails permanently.
func (s *Service) Report(ctx context.Context, id Identity, res TaskResult) (Task, error) {
	task, err := s.taskFor(ctx, id.Node.ID, res.TaskID)
	if err != nil {
		return Task{}, err
	}
	if task.State != TaskDispatched {
		return Task{}, ErrTaskNotClaimed
	}

	result := truncate(res.Result, MaxResultLen)
	failure := truncate(res.Error, MaxErrorLen)
	now := s.now()

	if res.Success {
		if err := s.db.UpdateVersioned(ctx,
			`UPDATE node_tasks SET state = ?, finished_at = ?, exit_code = ?,
				result = ?, error = '', version = version + 1
			 WHERE id = ? AND version = ?`,
			string(TaskSucceeded), unixOf(now), res.ExitCode, result,
			task.ID, task.Version); err != nil {
			return Task{}, wrapVersioned(err, "record task result")
		}
		task.State = TaskSucceeded
		task.FinishedAt = now
		task.ExitCode = res.ExitCode
		task.Result = result
		task.Error = ""
		task.Version++

		s.audit("nodes.task_succeeded", "node", task.NodeID,
			"task_id", task.ID, "action", task.Action, "attempt", task.Attempts)
		return task, nil
	}

	if failure == "" {
		failure = "node reported failure"
	}
	return s.failAttempt(ctx, task, res.ExitCode, result, failure, now, "node")
}

// failAttempt records a failed attempt, requeueing with backoff while the
// attempt budget allows and failing the task permanently once it does not.
func (s *Service) failAttempt(ctx context.Context, task Task, exitCode int64,
	result, failure string, now time.Time, actor string) (Task, error) {

	if task.Attempts < task.MaxAttempts {
		next := now.Add(RetryDelay(task.Attempts))
		if err := s.db.UpdateVersioned(ctx,
			`UPDATE node_tasks SET state = ?, next_attempt_at = ?, claimed_at = 0,
				deadline_at = 0, exit_code = ?, result = ?, error = ?, version = version + 1
			 WHERE id = ? AND version = ?`,
			string(TaskQueued), unixOf(next), exitCode, result, failure,
			task.ID, task.Version); err != nil {
			return Task{}, wrapVersioned(err, "requeue node task")
		}
		task.State = TaskQueued
		task.NextAttemptAt = next
		task.ClaimedAt = time.Time{}
		task.DeadlineAt = time.Time{}
		task.ExitCode = exitCode
		task.Result = result
		task.Error = failure
		task.Version++

		s.audit("nodes.task_retry", actor, task.NodeID,
			"task_id", task.ID, "action", task.Action, "attempt", task.Attempts,
			"max_attempts", task.MaxAttempts)
		return task, nil
	}

	if err := s.db.UpdateVersioned(ctx,
		`UPDATE node_tasks SET state = ?, finished_at = ?, claimed_at = 0,
			deadline_at = 0, exit_code = ?, result = ?, error = ?, version = version + 1
		 WHERE id = ? AND version = ?`,
		string(TaskFailed), unixOf(now), exitCode, result, failure,
		task.ID, task.Version); err != nil {
		return Task{}, wrapVersioned(err, "fail node task")
	}
	task.State = TaskFailed
	task.FinishedAt = now
	task.ClaimedAt = time.Time{}
	task.DeadlineAt = time.Time{}
	task.ExitCode = exitCode
	task.Result = result
	task.Error = failure
	task.Version++

	s.audit("nodes.task_failed", actor, task.NodeID,
		"task_id", task.ID, "action", task.Action, "attempts", task.Attempts)
	return task, nil
}

// RetryDelay returns the backoff before the next attempt: RetryBaseDelay
// doubled per completed attempt, capped at RetryMaxDelay.
func RetryDelay(attempts int64) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := RetryBaseDelay
	for i := int64(1); i < attempts; i++ {
		delay *= 2
		if delay >= RetryMaxDelay {
			return RetryMaxDelay
		}
	}
	if delay > RetryMaxDelay {
		return RetryMaxDelay
	}
	return delay
}

// ReapTasks fails or requeues every claimed task whose deadline has passed,
// which is how a node that accepted work and then vanished stops holding it
// forever. It returns how many tasks it touched.
func (s *Service) ReapTasks(ctx context.Context) (int, error) {
	now := s.now()
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+taskColumns+` FROM node_tasks
		 WHERE state = ? AND deadline_at > 0 AND deadline_at <= ?
		 ORDER BY deadline_at, id`,
		string(TaskDispatched), unixOf(now))
	if err != nil {
		return 0, wrapInternal(err, "list timed out tasks")
	}

	var stale []Task
	for rows.Next() {
		task, err := scanTaskRow(rows)
		if err != nil {
			_ = rows.Close()
			return 0, wrapInternal(err, "scan node task")
		}
		stale = append(stale, task)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, wrapInternal(err, "list timed out tasks")
	}
	if err := rows.Close(); err != nil {
		return 0, wrapInternal(err, "list timed out tasks")
	}

	reaped := 0
	for _, task := range stale {
		if _, err := s.failAttempt(ctx, task, 0, task.Result, "attempt timed out", now, "scheduler"); err != nil {
			// Another writer finished the task first; leave it alone.
			if errors.Is(err, ErrConcurrentUpdate) {
				continue
			}
			return reaped, err
		}
		reaped++
	}
	return reaped, nil
}

// CancelTask withdraws a task that has not finished. Cancelling an already
// terminal task is a conflict rather than a silent success, so an operator
// is never told they stopped something that had already run.
func (s *Service) CancelTask(ctx context.Context, taskID, reason, actor string) (Task, error) {
	task, err := s.Task(ctx, taskID)
	if err != nil {
		return Task{}, err
	}
	if task.Terminal() {
		return Task{}, ErrInvalidTransition
	}

	now := s.now()
	clean := truncate(reason, MaxReasonLen)
	if clean == "" {
		clean = "cancelled by operator"
	}
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE node_tasks SET state = ?, finished_at = ?, claimed_at = 0,
			deadline_at = 0, error = ?, version = version + 1
		 WHERE id = ? AND version = ?`,
		string(TaskCancelled), unixOf(now), clean, task.ID, task.Version); err != nil {
		return Task{}, wrapVersioned(err, "cancel node task")
	}
	task.State = TaskCancelled
	task.FinishedAt = now
	task.ClaimedAt = time.Time{}
	task.DeadlineAt = time.Time{}
	task.Error = clean
	task.Version++

	s.audit("nodes.task_cancelled", truncate(actor, MaxNodeNameLen), task.NodeID,
		"task_id", task.ID, "action", task.Action, "reason", clean)
	return task, nil
}

// cancelQueued withdraws every task still waiting for a node, which is what
// a drain and a removal do to the node's backlog. Work the node already
// holds is left alone so an operation in flight is not orphaned.
func (s *Service) cancelQueued(ctx context.Context, nodeID, reason string) (int64, error) {
	now := s.now()
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE node_tasks SET state = ?, finished_at = ?, error = ?, version = version + 1
		 WHERE node_id = ? AND state = ?`,
		string(TaskCancelled), unixOf(now), truncate(reason, MaxReasonLen),
		nodeID, string(TaskQueued))
	if err != nil {
		return 0, wrapInternal(err, "cancel queued tasks")
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, wrapInternal(err, "cancel queued tasks")
	}
	return rows, nil
}

// Task returns one task by id.
func (s *Service) Task(ctx context.Context, taskID string) (Task, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+taskColumns+` FROM node_tasks WHERE id = ?`, taskID)
	task, err := scanTaskRow(row)
	if database.IsNotFound(err) {
		return Task{}, ErrTaskNotFound
	}
	if err != nil {
		return Task{}, wrapInternal(err, "load node task")
	}
	return task, nil
}

// taskFor returns a task only when it belongs to the given node, so a node
// can neither read nor report another node's work.
func (s *Service) taskFor(ctx context.Context, nodeID, taskID string) (Task, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+taskColumns+` FROM node_tasks WHERE id = ? AND node_id = ?`, taskID, nodeID)
	task, err := scanTaskRow(row)
	if database.IsNotFound(err) {
		return Task{}, ErrTaskNotFound
	}
	if err != nil {
		return Task{}, wrapInternal(err, "load node task")
	}
	return task, nil
}

// ListTasks returns a node's tasks, newest first, optionally filtered by
// state.
func (s *Service) ListTasks(ctx context.Context, nodeID string, state TaskState, limit int) ([]Task, error) {
	if err := ValidateNodeID(nodeID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	query := `SELECT ` + taskColumns + ` FROM node_tasks WHERE node_id = ?`
	args := []any{nodeID}
	if state != "" {
		query += ` AND state = ?`
		args = append(args, string(state))
	}
	query += ` ORDER BY created_at DESC, id LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query, args...)
	if err != nil {
		return nil, wrapInternal(err, "list node tasks")
	}
	defer func() { _ = rows.Close() }()

	var out []Task
	for rows.Next() {
		task, err := scanTaskRow(rows)
		if err != nil {
			return nil, wrapInternal(err, "scan node task")
		}
		out = append(out, task)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapInternal(err, "list node tasks")
	}
	return out, nil
}
