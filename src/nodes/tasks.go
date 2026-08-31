package nodes

import (
	"context"

	"github.com/webappsgo/cashp/src/scheduler"
)

// TaskRegistrar is the narrow slice of *scheduler.Scheduler this package
// needs. Declaring it locally keeps the dependency one-way and lets a test
// capture the registered tasks without starting a scheduler.
type TaskRegistrar interface {
	// Register adds a recurring task.
	Register(t scheduler.Task) error
}

// Scheduler task names, exported so the admin panel can address them.
const (
	// TaskLivenessSweep reclassifies nodes from their last contact.
	TaskLivenessSweep = "nodes_liveness_sweep"
	// TaskReaper requeues or fails timed-out node tasks.
	TaskReaper = "nodes_task_reaper"
	// TaskTokenExpiry closes lapsed enrollment tokens.
	TaskTokenExpiry = "nodes_token_expiry"
)

// SchedulerTasks returns this package's recurring work. Every task is
// ClusterWide so exactly one cluster node runs it per window, which is
// precisely the guarantee the shared control plane exists to provide and the
// reason this package never starts a goroutine loop of its own.
func (s *Service) SchedulerTasks() []scheduler.Task {
	return []scheduler.Task{
		{
			Name:        TaskLivenessSweep,
			Title:       "Node liveness sweep",
			Description: "Marks nodes degraded after 90s and offline after 5m of silence",
			Schedule:    "@every 30s",
			ClusterWide: true,
			Run: func(ctx context.Context) error {
				result, err := s.SweepLiveness(ctx)
				if err != nil {
					return err
				}
				if result.Degraded > 0 || result.Offline > 0 {
					s.audit("nodes.liveness_sweep", "scheduler", "",
						"degraded", result.Degraded, "offline", result.Offline)
				}
				return nil
			},
		},
		{
			Name:        TaskReaper,
			Title:       "Node task reaper",
			Description: "Requeues or fails node tasks whose attempt deadline passed",
			Schedule:    "@every 1m",
			ClusterWide: true,
			Run: func(ctx context.Context) error {
				reaped, err := s.ReapTasks(ctx)
				if err != nil {
					return err
				}
				if reaped > 0 {
					s.audit("nodes.tasks_reaped", "scheduler", "", "count", reaped)
				}
				return nil
			},
		},
		{
			Name:        TaskTokenExpiry,
			Title:       "Node enrollment token expiry",
			Description: "Closes enrollment tokens that have passed their expiry",
			Schedule:    "@every 1h",
			CatchUp:     true,
			ClusterWide: true,
			Run: func(ctx context.Context) error {
				_, err := s.ExpireEnrollmentTokens(ctx)
				return err
			},
		},
	}
}

// RegisterTasks registers this package's recurring work with the scheduler.
func (s *Service) RegisterTasks(reg TaskRegistrar) error {
	for _, task := range s.SchedulerTasks() {
		if err := reg.Register(task); err != nil {
			return wrapInternal(err, "register node scheduler task")
		}
	}
	return nil
}
