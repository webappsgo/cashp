package dbservice

import (
	"context"
	"log/slog"
)

// Version selection and upgrades. Which of the two paths an engine takes is
// the adapter's decision: engines whose on-disk format migrates itself restart
// on the new image, and engines whose format is not forward compatible are
// dumped with their own tooling and reloaded on the new version.

// Versions returns the engine versions this build offers, newest last.
func (s *Service) Versions(engine Engine) ([]string, error) {
	info, err := InfoFor(engine)
	if err != nil {
		return nil, err
	}
	return info.Versions, nil
}

// UpgradePath returns the versions an instance can currently move to. Only
// forward moves are offered: no engine in the registry supports reloading its
// data directory on an older release.
func (s *Service) UpgradePath(ctx context.Context, tenantID, instanceID string) ([]string, error) {
	inst, err := s.live(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	a, err := adapterFor(inst.Engine)
	if err != nil {
		return nil, err
	}
	current := versionIndex(a, inst.EngineVersion)
	versions := a.versions()
	out := make([]string, 0, len(versions))
	for i, v := range versions {
		if i > current {
			out = append(out, v)
		}
	}
	return out, nil
}

// Upgrade moves an instance to a newer engine version.
func (s *Service) Upgrade(ctx context.Context, req UpgradeRequest) (*Instance, error) {
	inst, err := s.running(ctx, req.TenantID, req.InstanceID)
	if err != nil {
		return nil, err
	}
	a, err := adapterFor(inst.Engine)
	if err != nil {
		return nil, err
	}
	target, err := resolveVersion(a, req.TargetVersion)
	if err != nil {
		return nil, err
	}
	if target == inst.EngineVersion {
		return nil, ErrConflict("That instance already runs that version.")
	}
	if versionIndex(a, target) < versionIndex(a, inst.EngineVersion) {
		return nil, ErrUnsupported(inst.Engine, "moving an instance back to an older version")
	}
	if inst.Role == RoleReplica {
		return nil, ErrConflict("Upgrade the primary instead: a replica follows its primary's version.")
	}
	replicas, err := s.store.ListReplicas(ctx, inst.TenantID, inst.ID)
	if err != nil {
		return nil, err
	}
	if len(replicas) > 0 {
		return nil, ErrConflict("Remove this instance's replicas before upgrading it.")
	}
	cred, err := s.store.GetAdminCredential(ctx, inst.TenantID, inst.ID)
	if err != nil {
		return nil, err
	}
	password, err := s.decrypt(cred.Secret)
	if err != nil {
		return nil, err
	}

	from := inst.EngineVersion
	inst.State = StateUpgrading
	inst.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		return nil, err
	}
	switch a.upgradeStrategy() {
	case StrategyInPlace:
		err = s.upgradeInPlace(ctx, a, inst, password, target)
	case StrategyDumpRestore:
		err = s.upgradeByDump(ctx, a, inst, password, target, req.Actor)
	default:
		err = ErrUnsupported(inst.Engine, "changing the engine version")
	}
	if err != nil {
		return nil, s.markFailed(ctx, inst, err)
	}
	s.audit(req.Actor, "database.instance.upgraded", inst,
		slog.String("from_version", from), slog.String("to_version", target))
	return s.reload(ctx, inst)
}

// upgradeInPlace replaces the container with one built on the new image and
// lets the engine migrate its own data directory. The volume is untouched.
func (s *Service) upgradeInPlace(ctx context.Context, a adapter, inst *Instance, password, target string) error {
	if err := s.orch.Stop(ctx, inst.ContainerID, defaultStopTimeout); err != nil {
		return ErrInternal(err, "That instance could not be stopped for the upgrade.")
	}
	if err := s.orch.Remove(ctx, inst.ContainerID, false); err != nil {
		return ErrInternal(err, "That instance could not be rebuilt for the upgrade.")
	}
	inst.ContainerID = ""
	inst.EngineVersion = target
	return s.createContainer(ctx, a, inst, password, nil)
}

// upgradeByDump dumps the whole instance with the engine's own tooling,
// rebuilds it from an empty data directory on the new version and reloads the
// dump. Account passwords are re-applied afterwards because a cluster dump
// deliberately carries no password material.
func (s *Service) upgradeByDump(ctx context.Context, a adapter, inst *Instance, password, target, actor string) error {
	if s.backups == nil {
		return ErrNotConfigured("the backup repository")
	}
	inst.State = StateRunning
	rec, err := s.Backup(ctx, BackupRequest{
		TenantID:   inst.TenantID,
		InstanceID: inst.ID,
		Actor:      actor,
	})
	if err != nil {
		return err
	}
	inst.State = StateUpgrading
	if err := s.orch.Stop(ctx, inst.ContainerID, defaultStopTimeout); err != nil {
		return ErrInternal(err, "That instance could not be stopped for the upgrade.")
	}
	if err := s.orch.Remove(ctx, inst.ContainerID, false); err != nil {
		return ErrInternal(err, "That instance could not be rebuilt for the upgrade.")
	}
	if err := s.orch.RemoveVolume(ctx, inst.VolumeName); err != nil {
		return ErrInternal(err, "That instance's storage could not be rebuilt for the upgrade.")
	}
	inst.ContainerID = ""
	inst.EngineVersion = target
	if err := s.createVolume(ctx, inst); err != nil {
		return err
	}
	if err := s.createContainer(ctx, a, inst, password, nil); err != nil {
		return err
	}

	reader, err := s.backups.Open(ctx, rec.ArtifactID)
	if err != nil {
		return ErrInternal(err, "That upgrade's dump could not be read.")
	}
	defer reader.Close()
	c := s.contextFor(a, inst, password)
	plan, err := a.restore(c, "", reader)
	if err != nil {
		return err
	}
	if plan.Online == nil {
		return ErrUnsupported(inst.Engine, "reloading a cluster dump")
	}
	if _, err := s.run(ctx, inst, *plan.Online); err != nil {
		return err
	}
	return s.reapplyPasswords(ctx, a, c, inst)
}

// reapplyPasswords re-sets every live account's password from the stored
// ciphertext. A cluster dump carries no password material by design, so
// without this step the tenant's own accounts would come back unusable.
func (s *Service) reapplyPasswords(ctx context.Context, a adapter, c engineCtx, inst *Instance) error {
	creds, err := s.store.ListCredentials(ctx, inst.TenantID, inst.ID)
	if err != nil {
		return err
	}
	for _, cred := range creds {
		password, err := s.decrypt(cred.Secret)
		if err != nil {
			return err
		}
		cmds, err := a.setPassword(c, cred.Username, cred.Database, password)
		if err != nil {
			return err
		}
		if _, err := s.runAll(ctx, inst, cmds); err != nil {
			return err
		}
	}
	return nil
}
