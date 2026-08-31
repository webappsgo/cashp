// Package reporter runs the agent's long-lived loops: enrollment, the
// heartbeat, metric collection and panel-assigned task execution. Every
// loop shares one outbound transport, so cluster failover happens once for
// all of them.
package reporter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/agent/collector"
	"github.com/webappsgo/cashp/src/agent/paths"
	"github.com/webappsgo/cashp/src/agent/settings"
	"github.com/webappsgo/cashp/src/agent/task"
	"github.com/webappsgo/cashp/src/agent/transport"
	"github.com/webappsgo/cashp/src/logging"
)

// TaskPollInterval is how often the agent asks the panel for work. Tasks
// are pulled, never pushed: the agent never listens on a port.
const TaskPollInterval = 15 * time.Second

// ClusterRefreshInterval is how often the cluster membership is refreshed
// from the active node.
const ClusterRefreshInterval = 5 * time.Minute

// MaxTasksPerPoll caps how much work one poll can hand the agent, so a
// misbehaving or compromised panel cannot saturate the node.
const MaxTasksPerPoll = 16

// Options configures a Reporter.
type Options struct {
	// Config is the loaded agent.yml.
	Config *settings.Config
	// Client is the authenticated outbound transport.
	Client *transport.Client
	// Overrides are the resolved directory overrides.
	Overrides paths.Overrides
	// Version is the agent build version reported to the panel.
	Version string
	// Logger receives operational messages.
	Logger *slog.Logger
}

// Reporter owns the agent's runtime loops.
type Reporter struct {
	cfg       *settings.Config
	client    *transport.Client
	overrides paths.Overrides
	version   string
	log       *slog.Logger
	state     *State
	executor  *task.Executor
	started   time.Time
}

// New validates the options and prepares the loops. Enrollment state is
// loaded here so a restart resumes an existing registration.
func New(opts Options) (*Reporter, error) {
	if opts.Config == nil {
		return nil, errors.New("reporter needs a configuration")
	}
	if opts.Client == nil {
		return nil, errors.New("reporter needs a transport client")
	}

	log := opts.Logger
	if log == nil {
		log = logging.L()
	}
	version := strings.TrimSpace(opts.Version)
	if version == "" {
		version = "devel"
	}

	state, err := LoadState(paths.StateFile(opts.Overrides))
	if err != nil {
		return nil, err
	}

	return &Reporter{
		cfg:       opts.Config,
		client:    opts.Client,
		overrides: opts.Overrides,
		version:   version,
		log:       log,
		state:     state,
		executor:  &task.Executor{AgentID: state.AgentID, Run: task.SystemRunner},
		started:   time.Now(),
	}, nil
}

// State returns the current enrollment record.
func (r *Reporter) State() *State {
	return r.state
}

// Identity describes this node to the panel, preferring the operator's
// configured values over what the host reports.
func (r *Reporter) Identity() transport.Identity {
	hostname := strings.TrimSpace(r.cfg.Identity.Hostname)
	if hostname == "" {
		if detected, err := os.Hostname(); err == nil {
			hostname = detected
		}
	}

	return transport.Identity{
		Hostname:    hostname,
		DisplayName: strings.TrimSpace(r.cfg.Identity.DisplayName),
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Version:     r.version,
		Tags:        r.cfg.Identity.Tags,
		Labels:      r.cfg.Identity.Labels,
	}
}

// Register enrolls this node and persists the resulting agent id. An agent
// that is already registered re-registers only when force is set, which is
// what the `register --force` command does.
func (r *Reporter) Register(ctx context.Context, force bool) (*State, error) {
	if r.state.Registered() && !force {
		return r.state, nil
	}

	identity := r.Identity()
	if identity.Hostname == "" {
		return nil, errors.New("cannot determine this node's hostname")
	}

	registration, err := r.client.Register(ctx, identity)
	if err != nil {
		return nil, err
	}

	r.state.applyRegistration(registration, r.client.ActiveServer())
	r.executor.AgentID = r.state.AgentID
	if err := SaveState(paths.StateFile(r.overrides), r.state); err != nil {
		return nil, err
	}

	r.log.Info("agent registered", "agent_id", r.state.AgentID, "scope", r.state.Scope)
	return r.state, nil
}

// RefreshCluster asks the active node for the current membership and stores
// it in both the live client and agent.yml, so a restart starts from the
// last known-good node list.
func (r *Reporter) RefreshCluster(ctx context.Context) error {
	cluster, err := r.client.Autodiscover(ctx)
	if err != nil {
		return err
	}

	primary := cluster.Primary
	if primary == "" {
		primary = r.client.Primary()
	}
	r.client.SetCluster(primary, cluster.Nodes)

	if primary == r.cfg.Server.Primary && sameList(cluster.Nodes, r.cfg.Server.Cluster) {
		return nil
	}
	r.cfg.Server.Primary = primary
	r.cfg.Server.Cluster = cluster.Nodes
	return settings.Save(paths.ConfigFile(r.overrides), r.cfg)
}

// Run drives every enabled loop until ctx is cancelled. It returns nil on a
// clean shutdown so the caller can distinguish a stop from a failure.
func (r *Reporter) Run(ctx context.Context) error {
	if err := r.ensureRegistered(ctx); err != nil {
		return err
	}
	if err := r.RefreshCluster(ctx); err != nil {
		r.log.Warn("cluster discovery failed", "error", err)
	}

	healthEvery := r.interval(r.cfg.Health.Interval, 30*time.Second)
	collectEvery := r.interval(r.cfg.Collection.Interval, 60*time.Second)

	heartbeat := newTicker(r.cfg.Health.Enabled, healthEvery)
	defer heartbeat.Stop()
	collect := newTicker(r.cfg.Collection.Enabled, collectEvery)
	defer collect.Stop()
	tasks := time.NewTicker(TaskPollInterval)
	defer tasks.Stop()
	refresh := time.NewTicker(ClusterRefreshInterval)
	defer refresh.Stop()

	r.log.Info("agent running",
		"agent_id", r.state.AgentID,
		"server", r.client.ActiveServer(),
		"heartbeat", healthEvery.String(),
		"collection", collectEvery.String(),
	)

	for {
		select {
		case <-ctx.Done():
			r.log.Info("agent stopping")
			return nil
		case <-heartbeat.C:
			if err := r.SendHeartbeat(ctx); err != nil {
				r.report("heartbeat", err)
			}
		case <-collect.C:
			if err := r.SendMetrics(ctx); err != nil {
				r.report("metrics", err)
			}
		case <-tasks.C:
			if err := r.RunTasks(ctx); err != nil {
				r.report("tasks", err)
			}
		case <-refresh.C:
			if err := r.RefreshCluster(ctx); err != nil {
				r.report("cluster", err)
			}
		}
	}
}

// SendHeartbeat reports liveness and records when it last succeeded.
func (r *Reporter) SendHeartbeat(ctx context.Context) error {
	beat := transport.Heartbeat{
		AgentID:  r.state.AgentID,
		Status:   "online",
		Uptime:   int64(time.Since(r.started).Seconds()),
		Version:  r.version,
		LoadOne:  loadOne(),
		TaskDone: r.state.TasksCompleted,
	}
	if err := r.client.SendHeartbeat(ctx, beat); err != nil {
		return err
	}

	r.state.LastHeartbeat = time.Now().UTC().Format(time.RFC3339)
	return SaveState(paths.StateFile(r.overrides), r.state)
}

// SendMetrics runs one collection cycle and uploads it. Collectors that
// fail are logged, and whatever was gathered is still sent.
func (r *Reporter) SendMetrics(ctx context.Context) error {
	samples, failures := collector.Collect()
	for _, failure := range failures {
		r.log.Warn("collector failed", "error", failure)
	}
	if len(samples) == 0 {
		return nil
	}

	batch := r.cfg.Collection.BatchSize
	if batch <= 0 {
		batch = len(samples)
	}

	for start := 0; start < len(samples); start += batch {
		end := start + batch
		if end > len(samples) {
			end = len(samples)
		}
		report := transport.MetricsReport{
			AgentID:   r.state.AgentID,
			Collected: time.Now().UTC().Format(time.RFC3339),
			Samples:   samples[start:end],
		}
		if err := r.client.SendReport(ctx, report); err != nil {
			return err
		}
	}
	return nil
}

// RunTasks pulls queued work, executes what passes validation and reports
// every outcome — including refusals, so the panel learns that a task was
// rejected rather than silently lost.
func (r *Reporter) RunTasks(ctx context.Context) error {
	queued, err := r.client.PollTasks(ctx, r.state.AgentID)
	if err != nil {
		return err
	}
	if len(queued) == 0 {
		return nil
	}
	if len(queued) > MaxTasksPerPoll {
		r.log.Warn("task batch truncated", "received", len(queued), "limit", MaxTasksPerPoll)
		queued = queued[:MaxTasksPerPoll]
	}

	for _, item := range queued {
		result := r.executor.Execute(ctx, item)
		if result.Status == transport.TaskStatusSucceeded {
			r.state.TasksCompleted++
		} else {
			r.log.Warn("task not completed", "task_id", result.TaskID, "status", result.Status, "error", result.Error)
		}
		if err := r.client.ReportTaskResult(ctx, result); err != nil {
			return err
		}
	}
	return SaveState(paths.StateFile(r.overrides), r.state)
}

// ensureRegistered enrolls the node, retrying on connection failures with
// the configured reconnect delay. Authentication failures are fatal: a
// rejected token will not start working on its own.
func (r *Reporter) ensureRegistered(ctx context.Context) error {
	if r.state.Registered() {
		r.executor.AgentID = r.state.AgentID
		return nil
	}

	delay := r.interval(r.cfg.Server.ReconnectDelay, 10*time.Second)
	for {
		_, err := r.Register(ctx, false)
		if err == nil {
			return nil
		}
		if errors.Is(err, transport.ErrUnauthorized) {
			return err
		}

		r.log.Warn("registration failed, retrying", "error", err, "retry_in", delay.String())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// report logs a loop failure without aborting the agent. A single failed
// cycle is normal during a panel restart or a cluster failover.
func (r *Reporter) report(loop string, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	r.log.Warn("loop cycle failed", "loop", loop, "error", err)
}

// interval parses a configured duration, falling back when it is unusable.
func (r *Reporter) interval(value string, fallback time.Duration) time.Duration {
	parsed, err := time.ParseDuration(settings.NormalizeDuration(value))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

// newTicker returns a ticker that never fires when the loop is disabled.
func newTicker(enabled bool, every time.Duration) *time.Ticker {
	ticker := time.NewTicker(every)
	if !enabled {
		ticker.Stop()
	}
	return ticker
}

// loadOne returns the one-minute load average, or zero where the platform
// does not expose one.
func loadOne() float64 {
	samples, err := collector.CollectCPU()
	if err != nil {
		return 0
	}
	for _, item := range samples {
		if item.Name == "cpu.load1" {
			return item.Value
		}
	}
	return 0
}

// sameList reports whether two node lists are identical in order and value.
func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

// Describe renders a one-line summary of the enrollment for status output.
func (r *Reporter) Describe() string {
	if !r.state.Registered() {
		return "not registered"
	}
	return fmt.Sprintf("%s (%s) via %s", r.state.AgentID, r.state.Scope, r.client.ActiveServer())
}
