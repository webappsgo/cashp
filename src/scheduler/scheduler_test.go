package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/notify"
)

// fakeClock is a manually advanced clock so schedule arithmetic in tests is
// deterministic and fast.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

// newFakeClock starts a clock at a fixed instant.
func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, time.March, 10, 1, 0, 0, 0, time.UTC)}
}

// now returns the current fake time.
func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// advance moves the fake clock forward.
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// counter is a concurrency-safe run counter for test tasks.
type counter struct {
	mu sync.Mutex
	n  int
}

// inc records one run.
func (c *counter) inc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
}

// value reports the number of recorded runs.
func (c *counter) value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// bindRequired attaches a no-op implementation to every built-in task that
// Start requires, so tests can focus on the behaviour under test.
func bindRequired(t *testing.T, s *Scheduler) {
	t.Helper()
	for _, spec := range Builtins() {
		if !spec.Required {
			continue
		}
		if err := s.Bind(spec.Name, func(ctx context.Context) error { return nil }); err != nil {
			t.Fatalf("Bind(%s) error: %v", spec.Name, err)
		}
	}
}

// newTestScheduler builds a scheduler with temporary directories, a fake
// clock and a fast tick.
func newTestScheduler(t *testing.T, dir string, clock *fakeClock) *Scheduler {
	t.Helper()
	return New(Options{
		StateDir:        dir,
		LogDir:          filepath.Join(dir, "logs"),
		NodeID:          "node-a",
		CatchUpWindow:   time.Hour,
		Location:        time.UTC,
		TickInterval:    5 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
		Now:             clock.now,
	})
}

// waitFor polls until cond holds or the deadline expires.
func waitFor(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// statusOf returns the status record for one task.
func statusOf(t *testing.T, s *Scheduler, name string) TaskStatus {
	t.Helper()
	for _, st := range s.Status() {
		if st.Name == name {
			return st
		}
	}
	t.Fatalf("task %q missing from Status()", name)
	return TaskStatus{}
}

func TestNewRegistersEveryBuiltin(t *testing.T) {
	s := New(Options{StateDir: t.TempDir(), Location: time.UTC})
	status := s.Status()
	if len(status) != len(Builtins()) {
		t.Fatalf("Status() has %d tasks, want %d", len(status), len(Builtins()))
	}
	for _, spec := range Builtins() {
		st := statusOf(t, s, spec.Name)
		if st.Schedule != spec.Schedule {
			t.Errorf("%s schedule = %q, want %q", spec.Name, st.Schedule, spec.Schedule)
		}
		if st.Enabled != spec.DefaultEnabled {
			t.Errorf("%s enabled = %t, want %t", spec.Name, st.Enabled, spec.DefaultEnabled)
		}
		if st.Bound {
			t.Errorf("%s must start unbound", spec.Name)
		}
	}
}

func TestStartFailsOnUnboundRequiredBuiltin(t *testing.T) {
	s := New(Options{StateDir: t.TempDir(), Location: time.UTC})
	err := s.Start(context.Background())
	if err == nil {
		t.Fatal("expected Start to fail with unbound required built-ins")
	}
	for _, spec := range Builtins() {
		if !spec.Required {
			continue
		}
		if !strings.Contains(err.Error(), spec.Name) {
			t.Errorf("error must name %s: %v", spec.Name, err)
		}
	}
}

func TestStartStopLifecycle(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock()
	s := newTestScheduler(t, dir, clock)
	bindRequired(t, s)

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	if err := s.Start(context.Background()); err == nil {
		t.Error("second Start must fail")
	}
	if err := s.Register(Task{Name: "late", Schedule: "@hourly", Run: func(ctx context.Context) error { return nil }}); err == nil {
		t.Error("Register after Start must fail")
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop error: %v", err)
	}
	// Stop is idempotent.
	if err := s.Stop(); err != nil {
		t.Fatalf("second Stop error: %v", err)
	}
	if _, err := os.Stat(statePath(dir)); err != nil {
		t.Fatalf("state file missing after Stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "logs", logFileName)); err != nil {
		t.Fatalf("log file missing after Stop: %v", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	s := New(Options{StateDir: t.TempDir(), Location: time.UTC})
	run := func(ctx context.Context) error { return nil }

	if err := s.Register(Task{Name: "  ", Schedule: "@hourly", Run: run}); err == nil {
		t.Error("empty name must fail")
	}
	if err := s.Register(Task{Name: "x", Schedule: "@hourly"}); err == nil {
		t.Error("missing Run must fail")
	}
	if err := s.Register(Task{Name: "x", Schedule: "nonsense", Run: run}); err == nil {
		t.Error("invalid schedule must fail")
	}
	if err := s.Register(Task{Name: "x", Schedule: "@hourly", Run: run}); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if err := s.Register(Task{Name: "x", Schedule: "@hourly", Run: run}); err == nil {
		t.Error("duplicate name must fail")
	}
	// Registering over a built-in replaces its definition and binds it.
	if err := s.Register(Task{Name: TaskGeoIPUpdate, Schedule: "@daily", Run: run}); err != nil {
		t.Fatalf("built-in override error: %v", err)
	}
	st := statusOf(t, s, TaskGeoIPUpdate)
	if st.Schedule != "@daily" || !st.Bound {
		t.Errorf("built-in override not applied: %+v", st)
	}
}

func TestBindUnknownTask(t *testing.T) {
	s := New(Options{StateDir: t.TempDir(), Location: time.UTC})
	if err := s.Bind("does_not_exist", func(ctx context.Context) error { return nil }); !errors.Is(err, ErrNotFound) {
		t.Errorf("Bind unknown = %v, want ErrNotFound", err)
	}
	if err := s.Bind(TaskSSLRenewal, nil); err == nil {
		t.Error("Bind with nil function must fail")
	}
}

func TestScheduledTaskRunsWhenDue(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock()
	s := newTestScheduler(t, dir, clock)
	bindRequired(t, s)

	runs := &counter{}
	if err := s.Register(Task{
		Name:     "unit_task",
		Schedule: "@every 15m",
		Run: func(ctx context.Context) error {
			runs.inc()
			return nil
		},
	}); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer s.Stop()

	clock.advance(16 * time.Minute)
	if !waitFor(t, func() bool { return runs.value() >= 1 }) {
		t.Fatal("task did not run after its schedule came due")
	}
	if !waitFor(t, func() bool { return statusOf(t, s, "unit_task").LastStatus == StatusSuccess }) {
		t.Fatal("task status did not become success")
	}
	st := statusOf(t, s, "unit_task")
	if st.RunCount != 1 {
		t.Errorf("RunCount = %d, want 1", st.RunCount)
	}
	if !st.NextRun.After(clock.now()) {
		t.Errorf("NextRun = %s, want after %s", st.NextRun, clock.now())
	}
}

func TestFailedTaskRecordsErrorAndRetryBackoff(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock()
	s := newTestScheduler(t, dir, clock)
	bindRequired(t, s)

	boom := errors.New("boom")
	if err := s.Register(Task{
		Name:     "failing_task",
		Schedule: "@daily",
		Run:      func(ctx context.Context) error { return boom },
	}); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer s.Stop()

	if err := s.RunNow(context.Background(), "failing_task"); err == nil {
		t.Fatal("RunNow must surface the task error")
	}
	st := statusOf(t, s, "failing_task")
	if st.LastStatus != StatusFailed || st.LastError != "boom" || st.FailCount != 1 {
		t.Fatalf("failure not recorded: %+v", st)
	}
	wantFirst := clock.now().Add(DefaultRetryDelay)
	if !st.NextRun.Equal(wantFirst) {
		t.Errorf("first retry at %s, want %s", st.NextRun, wantFirst)
	}
	if err := s.RunNow(context.Background(), "failing_task"); err == nil {
		t.Fatal("second RunNow must surface the task error")
	}
	st = statusOf(t, s, "failing_task")
	wantSecond := clock.now().Add(2 * DefaultRetryDelay)
	if !st.NextRun.Equal(wantSecond) {
		t.Errorf("second retry at %s, want %s (exponential backoff)", st.NextRun, wantSecond)
	}
}

// newTestNotifier builds a real *notify.Notifier backed by a throwaway
// SQLite database, so a dispatch can be observed through the store's dedup
// claims without a live SMTP server or webhook target.
func newTestNotifier(t *testing.T) *notify.Notifier {
	t.Helper()

	db, err := database.Open(database.Config{Driver: database.DriverSQLite, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	n, err := notify.New(notify.Options{DB: db, ConfigDir: t.TempDir(), AppName: "cashp"})
	if err != nil {
		t.Fatalf("new notifier: %v", err)
	}
	return n
}

func TestFailedTaskWithoutNotifierSkipsNotification(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock()
	s := newTestScheduler(t, dir, clock)
	bindRequired(t, s)

	if err := s.Register(Task{
		Name:     "failing_task",
		Schedule: "@daily",
		Run:      func(ctx context.Context) error { return errors.New("boom") },
	}); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer s.Stop()

	if err := s.RunNow(context.Background(), "failing_task"); err == nil {
		t.Fatal("RunNow must surface the task error")
	}
}

func TestFailedTaskNotifiesSchedulerError(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock()
	s := newTestScheduler(t, dir, clock)
	s.opts.Notifier = newTestNotifier(t)
	bindRequired(t, s)

	var seenExecutionID string
	if err := s.Register(Task{
		Name:     "failing_task",
		Schedule: "@daily",
		Run: func(ctx context.Context) error {
			seenExecutionID = ExecutionIDFromContext(ctx)
			return errors.New("boom")
		},
	}); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer s.Stop()

	if err := s.RunNow(context.Background(), "failing_task"); err == nil {
		t.Fatal("RunNow must surface the task error")
	}
	if seenExecutionID == "" {
		t.Fatal("expected the task to observe a non-empty ExecutionID via context")
	}

	ctx := context.Background()
	held, err := s.opts.Notifier.Store().DedupHeld(ctx, notify.EventSchedulerError+":"+seenExecutionID)
	if err != nil {
		t.Fatalf("dedup held: %v", err)
	}
	if !held {
		t.Fatal("expected scheduler_error to have been dispatched")
	}
}

func TestExecutionIDFromContextEmptyOutsideScheduler(t *testing.T) {
	if got := ExecutionIDFromContext(context.Background()); got != "" {
		t.Errorf("ExecutionIDFromContext = %q, want empty outside a scheduled run", got)
	}
}

func TestRunNowErrors(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock()
	s := newTestScheduler(t, dir, clock)
	bindRequired(t, s)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer s.Stop()

	if err := s.RunNow(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RunNow unknown = %v, want ErrNotFound", err)
	}
	if err := s.RunNow(context.Background(), TaskI2PHealth); err == nil {
		t.Error("RunNow on an unbound task must fail")
	}
}

func TestSetEnabledAndSetSchedule(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock()
	s := newTestScheduler(t, dir, clock)
	bindRequired(t, s)

	runs := &counter{}
	if err := s.Register(Task{
		Name:     "toggle_task",
		Schedule: "@every 1m",
		Disabled: true,
		Run: func(ctx context.Context) error {
			runs.inc()
			return nil
		},
	}); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer s.Stop()

	clock.advance(5 * time.Minute)
	time.Sleep(50 * time.Millisecond)
	if runs.value() != 0 {
		t.Fatalf("disabled task ran %d times", runs.value())
	}
	if err := s.SetEnabled("toggle_task", true); err != nil {
		t.Fatalf("SetEnabled error: %v", err)
	}
	clock.advance(2 * time.Minute)
	if !waitFor(t, func() bool { return runs.value() >= 1 }) {
		t.Fatal("enabled task did not run")
	}
	if err := s.SetSchedule("toggle_task", "@daily"); err != nil {
		t.Fatalf("SetSchedule error: %v", err)
	}
	if st := statusOf(t, s, "toggle_task"); st.Schedule != "@daily" {
		t.Errorf("schedule = %q, want @daily", st.Schedule)
	}
	if err := s.SetSchedule("toggle_task", "not a schedule"); err == nil {
		t.Error("invalid schedule must be rejected")
	}
	if err := s.SetEnabled("missing", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetEnabled unknown = %v, want ErrNotFound", err)
	}
	if err := s.SetSchedule("missing", "@daily"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetSchedule unknown = %v, want ErrNotFound", err)
	}
}

func TestCatchUpRunsMissedTaskWithinWindow(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock()

	// Persist a state file whose next run fell due 10 minutes ago, inside
	// the 1 hour catch-up window.
	st := newState()
	st.Tasks["missed_task"] = &TaskState{
		TaskID:     "missed_task",
		TaskName:   "missed_task",
		Schedule:   "@every 15m",
		LastStatus: StatusSuccess,
		LastRun:    clock.now().Add(-25 * time.Minute),
		NextRun:    clock.now().Add(-10 * time.Minute),
		Enabled:    true,
	}
	if err := saveState(statePath(dir), st); err != nil {
		t.Fatalf("saveState error: %v", err)
	}

	s := newTestScheduler(t, dir, clock)
	bindRequired(t, s)
	runs := &counter{}
	if err := s.Register(Task{
		Name:     "missed_task",
		Schedule: "@every 15m",
		CatchUp:  true,
		Run: func(ctx context.Context) error {
			runs.inc()
			return nil
		},
	}); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer s.Stop()

	if !waitFor(t, func() bool { return runs.value() == 1 }) {
		t.Fatalf("catch-up run did not happen, runs = %d", runs.value())
	}
}

func TestCatchUpSkipsRunOutsideWindow(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock()

	st := newState()
	st.Tasks["stale_task"] = &TaskState{
		TaskID:   "stale_task",
		TaskName: "stale_task",
		Schedule: "@every 15m",
		NextRun:  clock.now().Add(-6 * time.Hour),
		Enabled:  true,
	}
	if err := saveState(statePath(dir), st); err != nil {
		t.Fatalf("saveState error: %v", err)
	}

	s := newTestScheduler(t, dir, clock)
	bindRequired(t, s)
	runs := &counter{}
	if err := s.Register(Task{
		Name:     "stale_task",
		Schedule: "@every 15m",
		CatchUp:  true,
		Run: func(ctx context.Context) error {
			runs.inc()
			return nil
		},
	}); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer s.Stop()

	time.Sleep(100 * time.Millisecond)
	if runs.value() != 0 {
		t.Fatalf("run outside the catch-up window must be skipped, runs = %d", runs.value())
	}
}

func TestStatePersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock()

	first := newTestScheduler(t, dir, clock)
	bindRequired(t, first)
	if err := first.Register(Task{
		Name:     "persisted_task",
		Schedule: "@daily",
		Run:      func(ctx context.Context) error { return nil },
	}); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if err := first.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	if err := first.RunNow(context.Background(), "persisted_task"); err != nil {
		t.Fatalf("RunNow error: %v", err)
	}
	if err := first.SetEnabled(TaskBackupHourly, true); err != nil {
		t.Fatalf("SetEnabled error: %v", err)
	}
	if err := first.Stop(); err != nil {
		t.Fatalf("Stop error: %v", err)
	}

	second := newTestScheduler(t, dir, clock)
	bindRequired(t, second)
	if err := second.Register(Task{
		Name:     "persisted_task",
		Schedule: "@daily",
		Run:      func(ctx context.Context) error { return nil },
	}); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if err := second.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer second.Stop()

	st := statusOf(t, second, "persisted_task")
	if st.RunCount != 1 || st.LastStatus != StatusSuccess {
		t.Errorf("state did not survive restart: %+v", st)
	}
	if !statusOf(t, second, TaskBackupHourly).Enabled {
		t.Error("enabled flag did not survive restart")
	}
}

func TestClusterWideTaskSkippedWhenLockHeld(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock()
	lockDir := filepath.Join(dir, lockDirName)
	locker := NewFileLocker(lockDir)

	// Another node already holds the lock for this task.
	if ok, err := locker.Acquire(context.Background(), "cluster_task", "node-b", time.Minute); err != nil || !ok {
		t.Fatalf("pre-acquire = %t, %v", ok, err)
	}

	s := newTestScheduler(t, dir, clock)
	bindRequired(t, s)
	runs := &counter{}
	if err := s.Register(Task{
		Name:        "cluster_task",
		Schedule:    "@daily",
		ClusterWide: true,
		Run: func(ctx context.Context) error {
			runs.inc()
			return nil
		},
	}); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer s.Stop()

	if err := s.RunNow(context.Background(), "cluster_task"); err != nil {
		t.Fatalf("RunNow error: %v", err)
	}
	if runs.value() != 0 {
		t.Fatalf("task must not run while another node holds the lock, runs = %d", runs.value())
	}
	if st := statusOf(t, s, "cluster_task"); st.LastStatus != StatusSkipped {
		t.Errorf("LastStatus = %q, want %q", st.LastStatus, StatusSkipped)
	}

	// Once the other node releases, this node runs it and releases again.
	if err := locker.Release(context.Background(), "cluster_task", "node-b"); err != nil {
		t.Fatalf("Release error: %v", err)
	}
	if err := s.RunNow(context.Background(), "cluster_task"); err != nil {
		t.Fatalf("RunNow error: %v", err)
	}
	if runs.value() != 1 {
		t.Fatalf("runs = %d, want 1", runs.value())
	}
	if st := statusOf(t, s, "cluster_task"); st.LockedBy != "" {
		t.Errorf("lock not released in state: %+v", st)
	}
	if _, err := os.Stat(locker.lockPath("cluster_task")); !os.IsNotExist(err) {
		t.Errorf("lock file still present: %v", err)
	}
}

func TestContextCancellationStopsLoop(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock()
	s := newTestScheduler(t, dir, clock)
	bindRequired(t, s)

	runs := &counter{}
	if err := s.Register(Task{
		Name:     "cancel_task",
		Schedule: "@every 1m",
		Run: func(ctx context.Context) error {
			runs.inc()
			return nil
		},
	}); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
	clock.advance(10 * time.Minute)
	time.Sleep(50 * time.Millisecond)
	if runs.value() != 0 {
		t.Errorf("cancelled scheduler ran %d tasks", runs.value())
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop error: %v", err)
	}
}

func TestSchedulerWithoutStateDirRunsInMemory(t *testing.T) {
	clock := newFakeClock()
	s := New(Options{Location: time.UTC, TickInterval: 5 * time.Millisecond, Now: clock.now})
	bindRequired(t, s)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	if err := s.RunNow(context.Background(), TaskSessionCleanup); err != nil {
		t.Fatalf("RunNow error: %v", err)
	}
	if st := statusOf(t, s, TaskSessionCleanup); st.RunCount != 1 {
		t.Errorf("RunCount = %d, want 1", st.RunCount)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop error: %v", err)
	}
}

func TestLoggerWritesToFileOnly(t *testing.T) {
	dir := t.TempDir()
	l := newTaskLogger(dir)
	l.Printf("task %s started", TaskSSLRenewal)
	if err := l.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, logFileName))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "ssl_renewal started") {
		t.Errorf("log line missing: %q", string(data))
	}
	// Writing after Close must not panic and must not reach the console.
	l.Printf("after close")
	if err := l.Close(); err != nil {
		t.Fatalf("second Close error: %v", err)
	}
	// An empty log directory degrades to a discarding logger.
	discard := newTaskLogger("")
	discard.Printf("ignored")
	if err := discard.Close(); err != nil {
		t.Fatalf("discard Close error: %v", err)
	}
}
