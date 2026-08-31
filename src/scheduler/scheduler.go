package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/notify"
)

// Scheduler defaults from AI.md PART 19 § Task Configuration, § Retry
// Policy and § Shutdown Behavior.
const (
	// DefaultCatchUpWindow is how far back a missed run is still replayed on
	// startup.
	DefaultCatchUpWindow = time.Hour
	// DefaultMaxRetries is the number of retry attempts after a failure.
	DefaultMaxRetries = 3
	// DefaultRetryDelay is the base delay before the first retry; each
	// further attempt doubles it (5m, 10m, 20m).
	DefaultRetryDelay = 5 * time.Minute
	// DefaultShutdownTimeout is how long Stop waits for running tasks.
	DefaultShutdownTimeout = 30 * time.Second
	// DefaultTickInterval is how often the scheduler loop looks for due
	// tasks. One second keeps 30-second heartbeats and minute-aligned cron
	// tasks punctual at negligible cost.
	DefaultTickInterval = time.Second
	// DefaultTimezone is the scheduler timezone from AI.md PART 19.
	DefaultTimezone = "America/New_York"
	// lockDirName is the sub-directory of StateDir holding cluster task
	// locks when no external Locker is supplied.
	lockDirName = "locks"
)

// ErrNotFound is returned for operations naming a task that is not
// registered.
var ErrNotFound = errors.New("scheduler: task not found")

// Task is a unit of scheduled work.
type Task struct {
	// Name is the unique task identifier, for example "ssl_renewal".
	Name string
	// Schedule is a cron-style or interval expression per AI.md PART 19
	// § Schedule Format.
	Schedule string
	// CatchUp runs the task on startup when its window was missed inside the
	// catch-up window.
	CatchUp bool
	// ClusterWide restricts execution to exactly one node in the cluster.
	ClusterWide bool
	// Run performs the work. It must honour context cancellation so shutdown
	// stays graceful.
	Run func(ctx context.Context) error
	// Title is the human-readable name shown in the admin panel; the task
	// name is used when empty.
	Title string
	// Description explains what the task does.
	Description string
	// Disabled sets the task's first-run enabled state; persisted state wins
	// on later starts.
	Disabled bool
	// MaxRetries caps retry attempts after a failure. Zero uses
	// DefaultMaxRetries; a negative value disables retries.
	MaxRetries int
	// RetryDelay is the base retry delay. Zero uses DefaultRetryDelay.
	RetryDelay time.Duration
}

// Options configures a Scheduler.
type Options struct {
	// StateDir holds the persistent scheduler state file and, unless a
	// Locker is supplied, the cluster lock files.
	StateDir string
	// LogDir holds the scheduler log file. Scheduler activity is written
	// there and never to the console.
	LogDir string
	// NodeID identifies this node when competing for cluster task locks.
	NodeID string
	// CatchUpWindow is how far back a missed run is still replayed on
	// startup. Zero uses DefaultCatchUpWindow.
	CatchUpWindow time.Duration
	// Location is the timezone cron expressions are evaluated in. Nil
	// resolves DefaultTimezone, falling back to UTC when unavailable.
	Location *time.Location
	// Locker guards cluster-wide tasks. Nil installs a file-based locker
	// under StateDir; the database package supplies an advisory-lock
	// implementation once available.
	Locker Locker
	// LockTTL is the cluster lock timeout. Zero uses DefaultLockTTL.
	LockTTL time.Duration
	// ShutdownTimeout is how long Stop waits for running tasks. Zero uses
	// DefaultShutdownTimeout.
	ShutdownTimeout time.Duration
	// TickInterval is how often the loop scans for due tasks. Zero uses
	// DefaultTickInterval.
	TickInterval time.Duration
	// Now supplies the current time. Nil uses time.Now.
	Now func() time.Time
	// Notifier delivers scheduler_error notifications per AI.md PART 18's
	// decision matrix; nil disables notification entirely.
	Notifier *notify.Notifier
}

// executionIDKey is the context key a task's ExecutionID is stored under,
// so a task's own subsystem (backup, tlsmgr, ...) can attach it to a more
// specific notification and let it suppress the generic scheduler_error for
// the same run, per AI.md PART 18.
type executionIDKey struct{}

// WithExecutionID returns ctx carrying id as the current run's execution
// ID. The scheduler calls this once per task run; task code never needs to.
func WithExecutionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, executionIDKey{}, id)
}

// ExecutionIDFromContext returns the execution ID the scheduler attached to
// ctx for the current task run, or "" outside a scheduled run (for example
// a manual, non-scheduler-driven call).
func ExecutionIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(executionIDKey{}).(string)
	return id
}

// TaskStatus is a point-in-time view of one task for the admin panel and
// the scheduler API endpoints.
type TaskStatus struct {
	Name        string
	Title       string
	Schedule    string
	Description string
	Enabled     bool
	Running     bool
	Bound       bool
	Builtin     bool
	ClusterWide bool
	LastRun     time.Time
	LastStatus  string
	LastError   string
	NextRun     time.Time
	RunCount    int64
	FailCount   int64
	LockedBy    string
}

// taskEntry is the scheduler's internal record for a registered task.
type taskEntry struct {
	def      Task
	schedule Schedule
	builtin  bool
	required bool
	running  bool
	attempts int
}

// Scheduler runs registered tasks on their schedules. It is always running
// for the lifetime of the application: there is no enable/disable switch
// for the scheduler itself, only for individual tasks.
type Scheduler struct {
	opts Options

	mu    sync.Mutex
	tasks map[string]*taskEntry
	order []string
	state *state

	statePath string
	log       *taskLogger
	locker    Locker

	started bool
	stopped bool
	cancel  context.CancelFunc
	loopWG  sync.WaitGroup
	taskWG  sync.WaitGroup
}

// New creates a Scheduler pre-populated with every built-in task from AI.md
// PART 19. Built-in tasks have no implementation until Bind attaches one.
func New(opts Options) *Scheduler {
	if opts.CatchUpWindow <= 0 {
		opts.CatchUpWindow = DefaultCatchUpWindow
	}
	if opts.LockTTL <= 0 {
		opts.LockTTL = DefaultLockTTL
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = DefaultShutdownTimeout
	}
	if opts.TickInterval <= 0 {
		opts.TickInterval = DefaultTickInterval
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Location == nil {
		if loc, err := time.LoadLocation(DefaultTimezone); err == nil {
			opts.Location = loc
		} else {
			opts.Location = time.UTC
		}
	}
	if strings.TrimSpace(opts.NodeID) == "" {
		host, err := os.Hostname()
		if err != nil || host == "" {
			host = "node"
		}
		opts.NodeID = host
	}
	s := &Scheduler{
		opts:      opts,
		tasks:     make(map[string]*taskEntry),
		state:     newState(),
		statePath: statePath(opts.StateDir),
		log:       newTaskLogger(""),
		locker:    opts.Locker,
	}
	if s.locker == nil {
		if opts.StateDir == "" {
			s.locker = noopLocker{}
		} else {
			s.locker = NewFileLocker(filepath.Join(opts.StateDir, lockDirName))
		}
	}
	for _, spec := range builtinSpecs {
		def := spec.task()
		sched, err := ParseSchedule(def.Schedule, opts.Location)
		if err != nil {
			// The built-in table is a package constant; an unparsable entry
			// is a programming error and the task is left unschedulable
			// rather than silently dropped from Status.
			sched = nil
		}
		s.tasks[def.Name] = &taskEntry{def: def, schedule: sched, builtin: true, required: spec.Required}
		s.order = append(s.order, def.Name)
	}
	return s
}

// Register adds a task, or replaces a built-in task's definition when the
// name matches one. The implementation must be present and the schedule
// must parse.
func (s *Scheduler) Register(t Task) error {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return fmt.Errorf("scheduler: task name is required")
	}
	if t.Run == nil {
		return fmt.Errorf("scheduler: task %q has no Run function", name)
	}
	sched, err := ParseSchedule(t.Schedule, s.opts.Location)
	if err != nil {
		return err
	}
	t.Name = name
	if t.Title == "" {
		t.Title = name
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return fmt.Errorf("scheduler: cannot register %q after Start", name)
	}
	if existing, ok := s.tasks[name]; ok {
		if !existing.builtin {
			return fmt.Errorf("scheduler: task %q is already registered", name)
		}
		existing.def = t
		existing.schedule = sched
		return nil
	}
	s.tasks[name] = &taskEntry{def: t, schedule: sched}
	s.order = append(s.order, name)
	return nil
}

// Bind attaches an implementation to a built-in task, keeping its default
// schedule and cluster properties. It is how packages that own the actual
// work (tls, geoip, backup, update, cluster, ...) supply their function.
func (s *Scheduler) Bind(name string, run func(ctx context.Context) error) error {
	if run == nil {
		return fmt.Errorf("scheduler: bind %q: run function is nil", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.tasks[name]
	if !ok {
		return fmt.Errorf("scheduler: bind %q: %w", name, ErrNotFound)
	}
	entry.def.Run = run
	return nil
}

// SetSchedule replaces a task's schedule expression, recomputing its next
// run when the scheduler is already running.
func (s *Scheduler) SetSchedule(name, expr string) error {
	sched, err := ParseSchedule(expr, s.opts.Location)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.tasks[name]
	if !ok {
		return fmt.Errorf("scheduler: set schedule %q: %w", name, ErrNotFound)
	}
	entry.def.Schedule = expr
	entry.schedule = sched
	if st, ok := s.state.Tasks[name]; ok {
		st.Schedule = expr
		st.NextRun = sched.Next(s.opts.Now())
	}
	return s.persistLocked()
}

// SetEnabled enables or disables a task. The scheduler itself is always
// running; only individual tasks can be toggled.
func (s *Scheduler) SetEnabled(name string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.tasks[name]
	if !ok {
		return fmt.Errorf("scheduler: set enabled %q: %w", name, ErrNotFound)
	}
	st := s.stateForLocked(entry)
	st.Enabled = enabled
	if enabled && entry.schedule != nil {
		st.NextRun = entry.schedule.Next(s.opts.Now())
	}
	s.log.Printf("task %s enabled=%t", name, enabled)
	return s.persistLocked()
}

// Start validates the registry, restores persistent state, replays missed
// runs inside the catch-up window and begins the scheduler loop. It returns
// once the loop is running; the loop stops when ctx is cancelled or Stop is
// called.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: already started")
	}
	if missing := s.missingRequiredLocked(); len(missing) > 0 {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: required built-in task(s) without a bound implementation: %s", strings.Join(missing, ", "))
	}
	s.mu.Unlock()

	if s.opts.StateDir != "" {
		if err := os.MkdirAll(s.opts.StateDir, 0o750); err != nil {
			return fmt.Errorf("scheduler: create state dir %s: %w", s.opts.StateDir, err)
		}
	}
	s.log = newTaskLogger(s.opts.LogDir)

	s.mu.Lock()
	if s.opts.StateDir != "" {
		loaded, loadErr := loadState(s.statePath)
		if loadErr != nil {
			s.log.Printf("state reset: %v", loadErr)
		}
		s.state = loaded
	}
	now := s.opts.Now()
	missed := s.syncStateLocked(now)
	for _, name := range s.order {
		entry := s.tasks[name]
		if entry.schedule == nil || entry.def.Run == nil {
			continue
		}
		st := s.stateForLocked(entry)
		if st.Enabled {
			st.NextRun = entry.schedule.Next(now)
		}
	}
	for _, name := range s.order {
		entry := s.tasks[name]
		if entry.def.Run != nil || !entry.builtin {
			continue
		}
		spec, _ := Builtin(name)
		if spec.Conditional != "" {
			s.log.Printf("built-in task %s has no implementation bound; it runs only when %s", name, spec.Conditional)
			continue
		}
		s.log.Printf("built-in task %s has no implementation bound; it will not run", name)
	}
	if err := s.persistLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.started = true
	s.stopped = false
	s.mu.Unlock()

	s.log.Printf("scheduler started node=%s tasks=%d timezone=%s catch_up_window=%s", s.opts.NodeID, len(s.order), s.opts.Location, s.opts.CatchUpWindow)

	if len(missed) > 0 {
		s.taskWG.Add(1)
		go s.replayMissed(runCtx, missed)
	}

	s.loopWG.Add(1)
	go s.loop(runCtx)
	return nil
}

// Stop halts dispatching, waits for running tasks up to the shutdown
// timeout, releases this node's cluster locks and saves state.
func (s *Scheduler) Stop() error {
	s.mu.Lock()
	if !s.started || s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	s.loopWG.Wait()

	done := make(chan struct{})
	go func() {
		s.taskWG.Wait()
		close(done)
	}()
	timer := time.NewTimer(s.opts.ShutdownTimeout)
	defer timer.Stop()
	timedOut := false
	select {
	case <-done:
	case <-timer.C:
		timedOut = true
		s.log.Printf("shutdown timeout after %s; forcing lock release", s.opts.ShutdownTimeout)
	}

	releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer releaseCancel()

	s.mu.Lock()
	now := s.opts.Now()
	for _, name := range s.order {
		entry := s.tasks[name]
		st, ok := s.state.Tasks[name]
		if !ok {
			continue
		}
		if timedOut && entry.running {
			// An interrupted task is marked for retry on the next start.
			entry.running = false
			st.LastStatus = StatusFailed
			st.LastError = "interrupted by shutdown"
			st.FailCount++
			st.NextRun = now
		}
		if st.LockedBy == s.opts.NodeID {
			if err := s.locker.Release(releaseCtx, name, s.opts.NodeID); err != nil {
				s.log.Printf("task %s lock release failed: %v", name, err)
			}
			st.LockedBy = ""
			st.LockedAt = time.Time{}
		}
	}
	err := s.persistLocked()
	s.started = false
	s.mu.Unlock()

	s.log.Printf("scheduler stopped node=%s", s.opts.NodeID)
	if closeErr := s.log.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	return err
}

// Status returns the current state of every registered task in
// registration order.
func (s *Scheduler) Status() []TaskStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TaskStatus, 0, len(s.order))
	for _, name := range s.order {
		entry := s.tasks[name]
		st := s.stateForLocked(entry).clone()
		title := entry.def.Title
		if title == "" {
			title = entry.def.Name
		}
		out = append(out, TaskStatus{
			Name:        entry.def.Name,
			Title:       title,
			Schedule:    entry.def.Schedule,
			Description: entry.def.Description,
			Enabled:     st.Enabled,
			Running:     entry.running,
			Bound:       entry.def.Run != nil,
			Builtin:     entry.builtin,
			ClusterWide: entry.def.ClusterWide,
			LastRun:     st.LastRun,
			LastStatus:  st.LastStatus,
			LastError:   st.LastError,
			NextRun:     st.NextRun,
			RunCount:    st.RunCount,
			FailCount:   st.FailCount,
			LockedBy:    st.LockedBy,
		})
	}
	return out
}

// RunNow triggers an immediate execution of a task, as the admin panel's
// "Run Now" action does. It returns once the run has completed.
func (s *Scheduler) RunNow(ctx context.Context, name string) error {
	s.mu.Lock()
	entry, ok := s.tasks[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: run %q: %w", name, ErrNotFound)
	}
	if entry.def.Run == nil {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: run %q: no implementation bound", name)
	}
	if entry.running {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: run %q: already running", name)
	}
	entry.running = true
	s.mu.Unlock()

	s.taskWG.Add(1)
	return s.execute(ctx, entry, true)
}

// missingRequiredLocked lists required built-in tasks with no bound
// implementation. The caller must hold s.mu.
func (s *Scheduler) missingRequiredLocked() []string {
	var missing []string
	for _, name := range s.order {
		entry := s.tasks[name]
		if entry.required && entry.def.Run == nil {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// stateForLocked returns the persistent record for a task, creating it on
// first sight. The caller must hold s.mu.
func (s *Scheduler) stateForLocked(entry *taskEntry) *TaskState {
	st, ok := s.state.Tasks[entry.def.Name]
	if !ok {
		title := entry.def.Title
		if title == "" {
			title = entry.def.Name
		}
		st = &TaskState{
			TaskID:     entry.def.Name,
			TaskName:   title,
			Schedule:   entry.def.Schedule,
			LastStatus: StatusPending,
			Enabled:    !entry.def.Disabled,
		}
		s.state.Tasks[entry.def.Name] = st
	}
	return st
}

// syncStateLocked reconciles persistent state with the registry and returns
// the tasks whose scheduled run was missed while the process was down and
// still falls inside the catch-up window, ordered by the time they were due.
// The caller must hold s.mu.
func (s *Scheduler) syncStateLocked(now time.Time) []string {
	type missedTask struct {
		name string
		due  time.Time
	}
	var missed []missedTask
	for _, name := range s.order {
		entry := s.tasks[name]
		st := s.stateForLocked(entry)
		title := entry.def.Title
		if title == "" {
			title = entry.def.Name
		}
		st.TaskID = entry.def.Name
		st.TaskName = title
		st.Schedule = entry.def.Schedule
		if st.LastStatus == "" {
			st.LastStatus = StatusPending
		}
		// A lock this node held before the restart is stale by definition.
		if st.LockedBy == s.opts.NodeID {
			st.LockedBy = ""
			st.LockedAt = time.Time{}
		}
		if !entry.def.CatchUp || !st.Enabled || entry.def.Run == nil || entry.schedule == nil {
			continue
		}
		due := st.NextRun
		if due.IsZero() {
			continue
		}
		if due.After(now) || now.Sub(due) > s.opts.CatchUpWindow {
			continue
		}
		missed = append(missed, missedTask{name: name, due: due})
	}
	sort.Slice(missed, func(i, j int) bool { return missed[i].due.Before(missed[j].due) })
	out := make([]string, 0, len(missed))
	for _, m := range missed {
		out = append(out, m.name)
	}
	return out
}

// replayMissed runs the tasks that were due while the process was down, in
// the order they were originally scheduled.
func (s *Scheduler) replayMissed(ctx context.Context, names []string) {
	defer s.taskWG.Done()
	for _, name := range names {
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		entry, ok := s.tasks[name]
		if !ok || entry.running || entry.def.Run == nil {
			s.mu.Unlock()
			continue
		}
		entry.running = true
		s.mu.Unlock()
		s.log.Printf("task %s catch-up run (missed while stopped)", name)
		s.taskWG.Add(1)
		if err := s.execute(ctx, entry, false); err != nil {
			s.log.Printf("task %s catch-up run failed: %v", name, err)
		}
	}
}

// loop is the scheduler's ticker loop. It runs until the context is
// cancelled.
func (s *Scheduler) loop(ctx context.Context) {
	defer s.loopWG.Done()
	ticker := time.NewTicker(s.opts.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.dispatchDue(ctx)
		}
	}
}

// dispatchDue starts every task whose next run time has arrived.
func (s *Scheduler) dispatchDue(ctx context.Context) {
	now := s.opts.Now()
	var due []*taskEntry
	s.mu.Lock()
	for _, name := range s.order {
		entry := s.tasks[name]
		if entry.running || entry.def.Run == nil || entry.schedule == nil {
			continue
		}
		st := s.stateForLocked(entry)
		if !st.Enabled || st.NextRun.IsZero() || now.Before(st.NextRun) {
			continue
		}
		entry.running = true
		due = append(due, entry)
	}
	s.mu.Unlock()
	for _, entry := range due {
		s.taskWG.Add(1)
		go func(e *taskEntry) {
			if err := s.execute(ctx, e, false); err != nil {
				s.log.Printf("task %s failed: %v", e.def.Name, err)
			}
		}(entry)
	}
}

// execute runs one task: acquire the cluster lock when the task is global,
// run the implementation, record the outcome and compute the next run. The
// caller must have marked the entry as running and added one count to
// taskWG.
func (s *Scheduler) execute(ctx context.Context, entry *taskEntry, manual bool) error {
	defer s.taskWG.Done()
	name := entry.def.Name

	if entry.def.ClusterWide {
		ok, err := s.locker.Acquire(ctx, name, s.opts.NodeID, s.opts.LockTTL)
		if err != nil {
			s.finish(entry, StatusFailed, err)
			return fmt.Errorf("scheduler: task %s lock: %w", name, err)
		}
		if !ok {
			s.log.Printf("task %s skipped: lock held by another node", name)
			s.finish(entry, StatusSkipped, nil)
			return nil
		}
		s.mu.Lock()
		st := s.stateForLocked(entry)
		st.LockedBy = s.opts.NodeID
		st.LockedAt = s.opts.Now().UTC()
		s.mu.Unlock()
		defer func() {
			if err := s.locker.Release(context.WithoutCancel(ctx), name, s.opts.NodeID); err != nil {
				s.log.Printf("task %s lock release failed: %v", name, err)
			}
			s.mu.Lock()
			if st, ok := s.state.Tasks[name]; ok {
				st.LockedBy = ""
				st.LockedAt = time.Time{}
			}
			if err := s.persistLocked(); err != nil {
				s.log.Printf("state save failed: %v", err)
			}
			s.mu.Unlock()
		}()
	}

	trigger := "scheduled"
	if manual {
		trigger = "manual"
	}
	start := s.opts.Now()
	executionID := name + "-" + start.UTC().Format("20060102T150405.000000000Z")
	ctx = WithExecutionID(ctx, executionID)
	s.log.Printf("task %s started (%s)", name, trigger)
	err := entry.def.Run(ctx)
	duration := s.opts.Now().Sub(start)
	if err != nil {
		s.log.Printf("task %s failed after %s: %v", name, duration, err)
		next := s.finish(entry, StatusFailed, err)
		s.notifySchedulerError(ctx, name, err, executionID, next)
		return fmt.Errorf("scheduler: task %s: %w", name, err)
	}
	s.log.Printf("task %s succeeded in %s", name, duration)
	s.finish(entry, StatusSuccess, nil)
	return nil
}

// notifySchedulerError dispatches scheduler_error for a failed task run,
// tolerating both an absent notifier and a delivery failure - a
// notification is never allowed to fail the task it describes. It is
// dispatched unconditionally; Notify's own suppression logic (matching
// ExecutionID against a more specific failure event such as backup_failed
// or ssl_renewal_failed) is what keeps a covered task to one notification.
func (s *Scheduler) notifySchedulerError(ctx context.Context, name string, taskErr error, executionID string, next time.Time) {
	if s.opts.Notifier == nil {
		return
	}

	nextRun := "unscheduled"
	if !next.IsZero() {
		nextRun = next.UTC().Format(time.RFC3339)
	}

	msg := notify.Message{
		Event:       notify.EventSchedulerError,
		ExecutionID: executionID,
		Vars: map[string]string{
			"task_name": name,
			"error":     taskErr.Error(),
			"next_run":  nextRun,
		},
	}

	if err := s.opts.Notifier.Notify(context.WithoutCancel(ctx), msg); err != nil {
		s.log.Printf("task %s notification failed: %v", name, err)
	}
}

// finish records the outcome of a run and schedules the next one, applying
// exponential retry backoff after a failure. It returns the next scheduled
// run time, zero when the task has no schedule.
func (s *Scheduler) finish(entry *taskEntry, status string, runErr error) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	st := s.stateForLocked(entry)
	entry.running = false
	st.LastStatus = status
	switch status {
	case StatusSuccess:
		entry.attempts = 0
		st.LastRun = now
		st.LastError = ""
		st.RunCount++
	case StatusFailed:
		st.LastRun = now
		st.FailCount++
		if runErr != nil {
			st.LastError = runErr.Error()
		}
	}
	if entry.schedule == nil {
		if err := s.persistLocked(); err != nil {
			s.log.Printf("state save failed: %v", err)
		}
		return time.Time{}
	}
	next := entry.schedule.Next(now)
	if status == StatusFailed {
		if retryAt, ok := s.retryTimeLocked(entry, now); ok && retryAt.Before(next) {
			next = retryAt
		}
	}
	st.NextRun = next
	if err := s.persistLocked(); err != nil {
		s.log.Printf("state save failed: %v", err)
	}

	return next
}

// retryTimeLocked returns the next retry time for a failed task using the
// exponential backoff from AI.md PART 19 § Retry Policy, or false when the
// attempt budget is exhausted. The caller must hold s.mu.
func (s *Scheduler) retryTimeLocked(entry *taskEntry, now time.Time) (time.Time, bool) {
	limit := entry.def.MaxRetries
	if limit == 0 {
		limit = DefaultMaxRetries
	}
	if limit < 0 {
		entry.attempts = 0
		return time.Time{}, false
	}
	if entry.attempts >= limit {
		entry.attempts = 0
		return time.Time{}, false
	}
	delay := entry.def.RetryDelay
	if delay <= 0 {
		delay = DefaultRetryDelay
	}
	backoff := delay << uint(entry.attempts)
	entry.attempts++
	return now.Add(backoff), true
}

// persistLocked writes scheduler state to disk. The caller must hold s.mu.
// With no state directory configured the scheduler runs in memory only,
// which is used by tests.
func (s *Scheduler) persistLocked() error {
	if s.opts.StateDir == "" {
		return nil
	}
	return saveState(s.statePath, s.state)
}
