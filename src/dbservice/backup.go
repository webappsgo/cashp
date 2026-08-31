package dbservice

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/webappsgo/cashp/src/backup"
)

// BackupRepository is the narrow contract this package needs from cashp's
// backup layer: somewhere to put a stream, get it back, delete it, and prune a
// scope under the project's retention policy. Deduplication, encryption and
// checksumming happen inside the repository, so a managed-database dump is
// stored exactly the way every other cashp backup is.
type BackupRepository interface {
	// Put stores a stream and returns the artifact it became.
	Put(ctx context.Context, meta ArtifactMeta, r io.Reader) (Artifact, error)
	// Open returns a reader over a stored artifact.
	Open(ctx context.Context, artifactID string) (io.ReadCloser, error)
	// Delete removes one stored artifact.
	Delete(ctx context.Context, artifactID string) error
	// Prune applies a retention policy to one scope and returns the artifact
	// identifiers it removed.
	Prune(ctx context.Context, scope string, policy backup.RetentionPolicy) ([]string, error)
}

// ArtifactMeta describes a stream being stored. Scope groups the artifacts
// retention is applied across, which for this package is one instance.
type ArtifactMeta struct {
	// TenantID is the owning tenant.
	TenantID string
	// Scope groups artifacts for retention, one scope per instance.
	Scope string
	// Name is a stable, human-readable artifact name.
	Name string
	// CreatedAt is when the dump was taken.
	CreatedAt time.Time
	// Labels are non-sensitive attributes stored alongside the artifact.
	Labels map[string]string
}

// Artifact is what the repository stored.
type Artifact struct {
	// ID identifies the stored artifact.
	ID string
	// SizeBytes is its stored size.
	SizeBytes int64
	// Checksum is the repository's content checksum.
	Checksum string
	// Encrypted records that the repository encrypted it at rest.
	Encrypted bool
}

// backupScope is the retention scope one instance's dumps share.
func backupScope(inst *Instance) string {
	return "dbservice/" + inst.TenantID + "/" + inst.ID
}

// Backup takes a native dump using the engine's own tooling and stores it in
// the backup repository, where it is deduplicated, encrypted and retained
// under the same policy as every other cashp backup.
func (s *Service) Backup(ctx context.Context, req BackupRequest) (*BackupRecord, error) {
	if s.backups == nil {
		return nil, ErrNotConfigured("the backup repository")
	}
	inst, err := s.running(ctx, req.TenantID, req.InstanceID)
	if err != nil {
		return nil, err
	}
	a, c, err := s.engineContext(ctx, inst)
	if err != nil {
		return nil, err
	}
	database := ""
	if req.Database != "" {
		if !a.capabilities().PerDatabaseDump {
			return nil, ErrUnsupported(inst.Engine, "backing up a single database")
		}
		database, err = s.resolveDatabase(ctx, a, inst, req.Database)
		if err != nil {
			return nil, err
		}
	}

	pr, pw := io.Pipe()
	cmds, err := a.dump(c, database, pw)
	if err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return nil, err
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := s.runAll(ctx, inst, cmds)
		done <- runErr
		_ = pw.CloseWithError(runErr)
	}()
	artifact, putErr := s.backups.Put(ctx, ArtifactMeta{
		TenantID:  inst.TenantID,
		Scope:     backupScope(inst),
		Name:      s.dumpName(inst, database),
		CreatedAt: s.now().UTC(),
		Labels: map[string]string{
			"engine":         string(inst.Engine),
			"engine_version": inst.EngineVersion,
			"instance":       inst.Name,
		},
	}, pr)
	_ = pr.Close()
	if runErr := <-done; runErr != nil {
		return nil, runErr
	}
	if putErr != nil {
		return nil, ErrInternal(putErr, "That backup could not be stored.")
	}

	rec := &BackupRecord{
		ID:            s.newID(),
		TenantID:      inst.TenantID,
		InstanceID:    inst.ID,
		ArtifactID:    artifact.ID,
		Engine:        inst.Engine,
		EngineVersion: inst.EngineVersion,
		Database:      database,
		SizeBytes:     artifact.SizeBytes,
		Checksum:      artifact.Checksum,
		Encrypted:     artifact.Encrypted,
		CreatedAt:     s.now().UTC(),
	}
	if err := s.store.CreateBackup(ctx, rec); err != nil {
		return nil, err
	}
	s.audit(req.Actor, "database.backup.created", inst,
		slog.String("backup_id", rec.ID), slog.String("database", database))
	return rec, nil
}

// dumpName builds a stable artifact name from the instance and the dumped
// database. It carries no secret and no host path.
func (s *Service) dumpName(inst *Instance, database string) string {
	stamp := s.now().UTC().Format("20060102T150405Z")
	if database == "" {
		return inst.Name + "-" + string(inst.Engine) + "-" + stamp
	}
	return inst.Name + "-" + database + "-" + string(inst.Engine) + "-" + stamp
}

// ListBackups returns an instance's restorable backups.
func (s *Service) ListBackups(ctx context.Context, tenantID, instanceID string) ([]*BackupRecord, error) {
	if _, err := s.live(ctx, tenantID, instanceID); err != nil {
		return nil, err
	}
	return s.store.ListBackups(ctx, tenantID, instanceID)
}

// Restore puts a stored dump back into an instance. It overwrites live data,
// so it requires an explicit confirmation flag and is audit logged. An engine
// without an online restore path is stopped, has its snapshot file replaced,
// and is started again.
func (s *Service) Restore(ctx context.Context, req RestoreRequest) error {
	if s.backups == nil {
		return ErrNotConfigured("the backup repository")
	}
	inst, err := s.live(ctx, req.TenantID, req.InstanceID)
	if err != nil {
		return err
	}
	if !req.Confirm {
		return ErrConfirmationRequired(inst.Name)
	}
	rec, err := s.store.GetBackup(ctx, req.TenantID, req.BackupID)
	if err != nil {
		return err
	}
	if rec.InstanceID != inst.ID {
		return ErrNotFound("backup")
	}
	if rec.Engine != inst.Engine {
		return ErrConflict("That backup was taken from a different database engine.")
	}
	a, c, err := s.engineContext(ctx, inst)
	if err != nil {
		return err
	}
	if versionIndex(a, rec.EngineVersion) > versionIndex(a, inst.EngineVersion) {
		return ErrConflict("That backup was taken from a newer version of the engine than this instance runs.")
	}
	reader, err := s.backups.Open(ctx, rec.ArtifactID)
	if err != nil {
		return ErrInternal(err, "That backup could not be read.")
	}
	defer reader.Close()

	plan, err := a.restore(c, rec.Database, reader)
	if err != nil {
		return err
	}
	switch {
	case plan.Online != nil:
		if inst.State != StateRunning {
			return ErrUnavailable("That instance is not running.")
		}
		if _, err := s.run(ctx, inst, *plan.Online); err != nil {
			return err
		}
	case plan.Offline != nil:
		if err := s.restoreOffline(ctx, a, inst, *plan.Offline, reader); err != nil {
			return err
		}
	default:
		return ErrUnsupported(inst.Engine, "restoring from a stored dump")
	}
	s.audit(req.Actor, "database.backup.restored", inst,
		slog.String("backup_id", rec.ID), slog.Bool("confirmed", true))
	return nil
}

// restoreOffline stops the engine, replaces its snapshot file and starts it
// again, waiting for it to answer its health probe before reporting success.
func (s *Service) restoreOffline(ctx context.Context, a adapter, inst *Instance, plan offlineRestore, r io.Reader) error {
	if err := s.orch.Stop(ctx, inst.ContainerID, defaultStopTimeout); err != nil {
		return ErrInternal(err, "That instance could not be stopped for the restore.")
	}
	spec := FileSpec{Path: plan.Path, Mode: plan.Mode, UID: plan.UID, GID: plan.GID}
	if err := s.orch.WriteFile(ctx, inst.ContainerID, spec, r); err != nil {
		return ErrInternal(err, "That backup could not be written to the instance.")
	}
	if err := s.orch.Start(ctx, inst.ContainerID); err != nil {
		return ErrInternal(err, "That instance could not be started after the restore.")
	}
	inst.State = StateRunning
	inst.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		return err
	}
	_, c, err := s.engineContext(ctx, inst)
	if err != nil {
		return err
	}
	return s.waitReady(ctx, a, c)
}

// PruneBackups applies the configured retention policy to one instance's
// stored dumps and tombstones the records the repository removed.
func (s *Service) PruneBackups(ctx context.Context, inst *Instance) error {
	if s.backups == nil {
		return ErrNotConfigured("the backup repository")
	}
	removed, err := s.backups.Prune(ctx, backupScope(inst), s.retention)
	if err != nil {
		return ErrInternal(err, "Stored backups could not be pruned.")
	}
	if len(removed) == 0 {
		return nil
	}
	gone := make(map[string]bool, len(removed))
	for _, id := range removed {
		gone[id] = true
	}
	records, err := s.store.ListBackups(ctx, inst.TenantID, inst.ID)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	for _, rec := range records {
		if !gone[rec.ArtifactID] {
			continue
		}
		if err := s.store.MarkBackupDeleted(ctx, rec.TenantID, rec.ID, now); err != nil {
			return err
		}
	}
	return nil
}

// DeleteBackup removes one stored dump at the tenant's request.
func (s *Service) DeleteBackup(ctx context.Context, tenantID, backupID, actor string) error {
	if s.backups == nil {
		return ErrNotConfigured("the backup repository")
	}
	rec, err := s.store.GetBackup(ctx, tenantID, backupID)
	if err != nil {
		return err
	}
	inst, err := s.store.GetInstance(ctx, tenantID, rec.InstanceID)
	if err != nil {
		return err
	}
	if err := s.backups.Delete(ctx, rec.ArtifactID); err != nil {
		return ErrInternal(err, "That backup could not be removed.")
	}
	if err := s.store.MarkBackupDeleted(ctx, tenantID, rec.ID, s.now().UTC()); err != nil {
		return err
	}
	s.audit(actor, "database.backup.deleted", inst, slog.String("backup_id", rec.ID))
	return nil
}
