package transport

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Registration is the panel's answer to a successful enrollment.
type Registration struct {
	AgentID    string `json:"agent_id"`
	Name       string `json:"name"`
	Scope      string `json:"scope"`
	ServerTime string `json:"server_time"`
}

// Identity is what the agent tells the panel about the node it runs on.
type Identity struct {
	Hostname    string            `json:"hostname"`
	DisplayName string            `json:"display_name,omitempty"`
	OS          string            `json:"os"`
	Arch        string            `json:"arch"`
	Version     string            `json:"version"`
	Tags        []string          `json:"tags,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// registerRequest is the enrollment body from AI.md PART 33 "Agent
// Registration API".
type registerRequest struct {
	Token       string            `json:"token"`
	Hostname    string            `json:"hostname"`
	DisplayName string            `json:"display_name,omitempty"`
	OS          string            `json:"os"`
	Arch        string            `json:"arch"`
	Version     string            `json:"version"`
	Tags        []string          `json:"tags,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// Cluster is the subset of /api/autodiscover the agent needs.
type Cluster struct {
	Primary    string           `json:"primary"`
	Nodes      []string         `json:"cluster"`
	APIVersion string           `json:"api_version"`
	ServerName string           `json:"server_name"`
	AgentMin   string           `json:"agent_min_version"`
	Builds     map[string]Build `json:"agent_versions"`
}

// Build is one published agent binary, keyed by "{os}-{arch}". It mirrors
// the cli_versions entries the CLI consumes.
type Build struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	URL     string `json:"url,omitempty"`
}

// BuildFor returns the published build for one platform key.
func (c *Cluster) BuildFor(key string) (Build, bool) {
	if c == nil || len(c.Builds) == 0 {
		return Build{}, false
	}
	build, ok := c.Builds[key]
	if !ok || strings.TrimSpace(build.Version) == "" {
		return Build{}, false
	}
	return build, true
}

// Heartbeat is the periodic liveness report.
type Heartbeat struct {
	AgentID  string  `json:"agent_id"`
	Status   string  `json:"status"`
	Uptime   int64   `json:"uptime_seconds"`
	Version  string  `json:"version"`
	LoadOne  float64 `json:"load_1m,omitempty"`
	Sent     string  `json:"sent_at"`
	TaskDone int     `json:"tasks_completed"`
}

// MetricsReport carries one collection cycle to the panel.
type MetricsReport struct {
	AgentID   string         `json:"agent_id"`
	Collected string         `json:"collected_at"`
	Samples   []MetricSample `json:"samples"`
}

// MetricSample is one named measurement.
type MetricSample struct {
	Name   string            `json:"name"`
	Value  float64           `json:"value"`
	Unit   string            `json:"unit,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// Task is a unit of work the panel has assigned to this agent. Every field
// is treated as untrusted input: the panel is a remote party from the
// node's point of view, so nothing here is executed without validation.
type Task struct {
	ID        string            `json:"id"`
	AgentID   string            `json:"agent_id"`
	Action    string            `json:"action"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	TimeoutMS int               `json:"timeout_ms,omitempty"`
}

// TaskResult reports the outcome of a task back to the panel.
type TaskResult struct {
	AgentID  string `json:"agent_id"`
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
	Finished string `json:"finished_at"`
}

// Task status values reported back to the panel.
const (
	TaskStatusSucceeded = "succeeded"
	TaskStatusFailed    = "failed"
	TaskStatusRejected  = "rejected"
)

// Autodiscover asks the active node for the current cluster membership.
// The endpoint is unversioned and unauthenticated by design.
func (c *Client) Autodiscover(ctx context.Context) (*Cluster, error) {
	env, err := c.Do(ctx, Request{Method: http.MethodGet, Path: AutodiscoverPath})
	if err != nil {
		return nil, err
	}

	cluster := &Cluster{}
	if err := env.Decode(cluster); err != nil {
		return nil, err
	}

	// Everything here came from the network: keep only usable node URLs.
	cluster.Primary = strings.TrimRight(strings.TrimSpace(cluster.Primary), "/")
	if ValidateServerURL(cluster.Primary) != nil {
		cluster.Primary = ""
	}
	cluster.Nodes = cleanURLs(cluster.Nodes)

	// A download URL the panel supplied is only honored when it is a usable
	// absolute URL; anything else falls back to the agent's own route.
	for key, build := range cluster.Builds {
		if build.URL != "" && ValidateServerURL(build.URL) != nil {
			build.URL = ""
			cluster.Builds[key] = build
		}
	}
	return cluster, nil
}

// Register enrolls this node. The token travels in both the Authorization
// header and the documented request body so the panel can bind the
// enrollment to the credential it issued.
func (c *Client) Register(ctx context.Context, identity Identity) (*Registration, error) {
	if strings.TrimSpace(identity.Hostname) == "" {
		return nil, errors.New("cannot register without a hostname")
	}

	body := registerRequest{
		Token:       c.token,
		Hostname:    identity.Hostname,
		DisplayName: identity.DisplayName,
		OS:          identity.OS,
		Arch:        identity.Arch,
		Version:     identity.Version,
		Tags:        identity.Tags,
		Labels:      identity.Labels,
	}

	env, err := c.Do(ctx, Request{
		Method: http.MethodPost,
		Path:   c.VersionedPath(c.BasePath() + "/register"),
		Body:   body,
	})
	if err != nil {
		return nil, err
	}

	registration := &Registration{}
	if err := env.Decode(registration); err != nil {
		return nil, err
	}
	if strings.TrimSpace(registration.AgentID) == "" {
		return nil, errors.New("the panel did not return an agent id")
	}
	return registration, nil
}

// SendHeartbeat reports liveness for an already-registered agent.
func (c *Client) SendHeartbeat(ctx context.Context, beat Heartbeat) error {
	if strings.TrimSpace(beat.AgentID) == "" {
		return errors.New("cannot send a heartbeat before registering")
	}
	beat.Sent = time.Now().UTC().Format(time.RFC3339)

	_, err := c.Do(ctx, Request{
		Method:     http.MethodPost,
		Path:       c.VersionedPath(c.BasePath() + "/{agent_id}/heartbeat"),
		PathParams: map[string]string{"agent_id": beat.AgentID},
		Body:       beat,
	})
	return err
}

// SendReport uploads one collection cycle.
func (c *Client) SendReport(ctx context.Context, report MetricsReport) error {
	if strings.TrimSpace(report.AgentID) == "" {
		return errors.New("cannot send a report before registering")
	}
	if len(report.Samples) == 0 {
		return nil
	}

	_, err := c.Do(ctx, Request{
		Method:     http.MethodPost,
		Path:       c.VersionedPath(c.BasePath() + "/{agent_id}/report"),
		PathParams: map[string]string{"agent_id": report.AgentID},
		Body:       report,
	})
	return err
}

// PollTasks fetches the tasks the panel has queued for this agent. Tasks
// addressed to a different agent are discarded here, before any of them
// reaches the executor.
func (c *Client) PollTasks(ctx context.Context, agentID string) ([]Task, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, errors.New("cannot poll for tasks before registering")
	}

	env, err := c.Do(ctx, Request{
		Method:     http.MethodGet,
		Path:       c.VersionedPath(c.BasePath() + "/{agent_id}/tasks"),
		PathParams: map[string]string{"agent_id": agentID},
	})
	if err != nil {
		return nil, err
	}

	tasks := []Task{}
	if len(env.Data) == 0 {
		return tasks, nil
	}
	if err := env.Decode(&tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

// ReportTaskResult returns the outcome of one task.
func (c *Client) ReportTaskResult(ctx context.Context, result TaskResult) error {
	if strings.TrimSpace(result.AgentID) == "" || strings.TrimSpace(result.TaskID) == "" {
		return errors.New("a task result needs both an agent id and a task id")
	}
	result.Finished = time.Now().UTC().Format(time.RFC3339)

	_, err := c.Do(ctx, Request{
		Method: http.MethodPost,
		Path:   c.VersionedPath(c.BasePath() + "/{agent_id}/tasks/{task_id}/result"),
		PathParams: map[string]string{
			"agent_id": result.AgentID,
			"task_id":  result.TaskID,
		},
		Body: result,
	})
	return err
}

// Ping verifies that the active node is reachable and that this agent's
// credential is accepted.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/healthz", SkipEnvelope: true})
	return err
}
