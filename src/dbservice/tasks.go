package dbservice

import (
	"context"
	"log/slog"

	"github.com/webappsgo/cashp/src/scheduler"
)

// Scheduled work. Every recurring job this package needs is handed to cashp's
// scheduler rather than run from a goroutine of its own, so a cluster runs each
// sweep exactly once and an operator can see, disable or reschedule it.
const (
	// TaskHealthSweep probes every managed instance.
	TaskHealthSweep = "dbservice_health_sweep"
	// TaskBackupSweep takes a native dump of every running instance.
	TaskBackupSweep = "dbservice_backup_sweep"
	// TaskBackupPrune applies the retention policy to stored dumps.
	TaskBackupPrune = "dbservice_backup_prune"
	// TaskCredentialRotation replaces tenant passwords that have aged out.
	TaskCredentialRotation = "dbservice_credential_rotation"
)

// Default schedules. Each is overridable through Options.
const (
	// defaultHealthSchedule probes managed engines every five minutes.
	defaultHealthSchedule = "@every 5m"
	// defaultBackupSchedule dumps managed engines nightly, before the prune.
	defaultBackupSchedule = "0 1 * * *"
	// defaultPruneSchedule applies retention after the nightly dumps.
	defaultPruneSchedule = "0 4 * * *"
	// defaultRotationSchedule looks for aged credentials once a day.
	defaultRotationSchedule = "0 5 * * *"
)

// Tasks returns the scheduled jobs this package contributes. The caller
// registers them with the process scheduler at wiring time.
func (s *Service) Tasks() []scheduler.Task {
	return []scheduler.Task{
		{
			Name:        TaskHealthSweep,
			Schedule:    scheduleOr(s.healthSchedule, defaultHealthSchedule),
			ClusterWide: true,
			Title:       "Managed database health sweep",
			Description: "Probes every managed database instance and records the result.",
			Run:         s.RunHealthSweep,
		},
		{
			Name:        TaskBackupSweep,
			Schedule:    scheduleOr(s.backupSchedule, defaultBackupSchedule),
			ClusterWide: true,
			CatchUp:     true,
			Title:       "Managed database backups",
			Description: "Takes a native dump of every running managed database instance.",
			Run:         s.RunBackupSweep,
		},
		{
			Name:        TaskBackupPrune,
			Schedule:    defaultPruneSchedule,
			ClusterWide: true,
			CatchUp:     true,
			Title:       "Managed database backup retention",
			Description: "Applies the retention policy to stored managed database dumps.",
			Run:         s.RunBackupPrune,
		},
		{
			Name:        TaskCredentialRotation,
			Schedule:    scheduleOr(s.rotationSchedule, defaultRotationSchedule),
			ClusterWide: true,
			CatchUp:     true,
			Title:       "Managed database credential rotation",
			Description: "Rotates managed database passwords that have reached their maximum age.",
			Run:         s.RunCredentialRotation,
		},
	}
}

// scheduleOr picks the configured expression, falling back to the package
// default when none was supplied.
func scheduleOr(configured, fallback string) string {
	if configured == "" {
		return fallback
	}
	return configured
}

// RunHealthSweep probes every live instance and records what it found. One
// unreachable instance never stops the sweep: the point of the sweep is to
// discover exactly that, so a failure is recorded and the loop continues.
func (s *Service) RunHealthSweep(ctx context.Context) error {
	instances, err := s.store.ListAllInstances(ctx)
	if err != nil {
		return err
	}
	for _, inst := range instances {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !inst.Alive() {
			continue
		}
		if _, err := s.refreshHealth(ctx, inst); err != nil {
			s.log.Warn("managed database health sweep could not probe an instance",
				slog.String("instance_id", inst.ID), slog.String("engine", string(inst.Engine)))
		}
	}
	return nil
}

// RunBackupSweep dumps every running instance into the backup repository.
func (s *Service) RunBackupSweep(ctx context.Context) error {
	if s.backups == nil {
		return ErrNotConfigured("the backup repository")
	}
	instances, err := s.store.ListAllInstances(ctx)
	if err != nil {
		return err
	}
	for _, inst := range instances {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !inst.Alive() || inst.State != StateRunning || inst.Role == RoleReplica {
			continue
		}
		_, err := s.Backup(ctx, BackupRequest{
			TenantID:   inst.TenantID,
			InstanceID: inst.ID,
			Actor:      "scheduler",
		})
		if err != nil {
			s.log.Warn("managed database backup sweep could not back up an instance",
				slog.String("instance_id", inst.ID), slog.String("engine", string(inst.Engine)))
		}
	}
	return nil
}

// RunBackupPrune applies the retention policy to every instance's dumps.
func (s *Service) RunBackupPrune(ctx context.Context) error {
	if s.backups == nil {
		return ErrNotConfigured("the backup repository")
	}
	instances, err := s.store.ListAllInstances(ctx)
	if err != nil {
		return err
	}
	for _, inst := range instances {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.PruneBackups(ctx, inst); err != nil {
			s.log.Warn("managed database retention could not prune an instance's backups",
				slog.String("instance_id", inst.ID))
		}
	}
	return nil
}

// RunCredentialRotation replaces every tenant password that has reached its
// maximum age. Administrative credentials are left alone: rotating one would
// break the replication pair that shares it, and it is never handed to a
// tenant in the first place.
func (s *Service) RunCredentialRotation(ctx context.Context) error {
	cutoff := s.now().UTC().Add(-s.rotationInterval)
	creds, err := s.store.ListCredentialsOlderThan(ctx, cutoff)
	if err != nil {
		return err
	}
	for _, cred := range creds {
		if err := ctx.Err(); err != nil {
			return err
		}
		if cred.Role == RoleAdmin {
			continue
		}
		_, err := s.RotateUser(ctx, cred.TenantID, cred.InstanceID, cred.Username, "scheduler")
		if err != nil {
			s.log.Warn("managed database rotation could not rotate a credential",
				slog.String("instance_id", cred.InstanceID))
		}
	}
	return nil
}
