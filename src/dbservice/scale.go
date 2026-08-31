package dbservice

import (
	"context"
	"io"
	"log/slog"
)

// Scale operations. Only what an engine genuinely supports is offered: an
// engine whose capability matrix says it has no replication returns the typed
// ErrUnsupported rather than pretending to add a replica.

// Replicas lists the read replicas of one instance.
func (s *Service) Replicas(ctx context.Context, tenantID, instanceID string) ([]*Instance, error) {
	inst, err := s.live(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	return s.store.ListReplicas(ctx, inst.TenantID, inst.ID)
}

// AddReplica provisions a read replica of an instance and joins it to the
// primary's topology using that engine's own replication mechanism.
func (s *Service) AddReplica(ctx context.Context, req ReplicaRequest) (*Instance, error) {
	primary, err := s.running(ctx, req.TenantID, req.InstanceID)
	if err != nil {
		return nil, err
	}
	if primary.Role != RolePrimary {
		return nil, ErrConflict("A replica cannot itself have replicas.")
	}
	a, err := adapterFor(primary.Engine)
	if err != nil {
		return nil, err
	}
	if !a.capabilities().Replicas {
		return nil, ErrUnsupported(primary.Engine, "read replicas")
	}
	if err := ValidateInstanceName(req.Name); err != nil {
		return nil, err
	}
	if err := s.checkInstanceQuota(ctx, req.TenantID); err != nil {
		return nil, err
	}
	if err := s.checkNameFree(ctx, req.TenantID, req.Name); err != nil {
		return nil, err
	}
	cred, err := s.store.GetAdminCredential(ctx, primary.TenantID, primary.ID)
	if err != nil {
		return nil, err
	}
	password, err := s.decrypt(cred.Secret)
	if err != nil {
		return nil, err
	}
	primaryCtx := s.contextFor(a, primary, password)

	replica, err := s.createInstance(ctx, a, primary.TenantID, req.Name, primary.EngineVersion,
		RoleReplica, primary.ID, primary.Limits)
	if err != nil {
		return nil, err
	}
	// A replica authenticates with the same administrative account as its
	// primary: after replication is established the two share one account
	// catalogue, so issuing a second password would leave the replica
	// unmanageable the moment it caught up.
	if err := s.storeAdminCredential(ctx, replica, password); err != nil {
		return nil, err
	}
	if err := s.bringUp(ctx, a, replica, password, &primaryCtx); err != nil {
		s.rollback(ctx, replica)
		return nil, err
	}
	replicaCtx := s.contextFor(a, replica, password)

	databases, err := s.databaseNames(ctx, primary)
	if err != nil {
		s.rollback(ctx, replica)
		return nil, err
	}
	if a.replicaNeedsSeed() {
		if err := s.seedReplica(ctx, a, primary, replica, primaryCtx, replicaCtx, databases); err != nil {
			s.rollback(ctx, replica)
			return nil, err
		}
	}
	plan, err := a.attachReplica(replicaCtx, primaryCtx, databases)
	if err != nil {
		s.rollback(ctx, replica)
		return nil, err
	}
	if err := s.runReplicaPlan(ctx, primary, replica, plan); err != nil {
		s.rollback(ctx, replica)
		return nil, err
	}
	s.audit(req.Actor, "database.replica.added", primary,
		slog.String("replica_id", replica.ID), slog.String("replica", replica.Name))
	return s.reload(ctx, replica)
}

// RemoveReplica detaches a replica from its primary and destroys it.
func (s *Service) RemoveReplica(ctx context.Context, req ReplicaRequest) error {
	primary, err := s.live(ctx, req.TenantID, req.InstanceID)
	if err != nil {
		return err
	}
	replica, err := s.live(ctx, req.TenantID, req.ReplicaID)
	if err != nil {
		return err
	}
	if replica.Role != RoleReplica || replica.PrimaryID != primary.ID {
		return ErrNotFound("replica")
	}
	a, err := adapterFor(primary.Engine)
	if err != nil {
		return err
	}
	if !a.capabilities().Replicas {
		return ErrUnsupported(primary.Engine, "read replicas")
	}
	cred, err := s.store.GetAdminCredential(ctx, primary.TenantID, primary.ID)
	if err != nil {
		return err
	}
	password, err := s.decrypt(cred.Secret)
	if err != nil {
		return err
	}
	primaryCtx := s.contextFor(a, primary, password)
	replicaCtx := s.contextFor(a, replica, password)
	databases, err := s.databaseNames(ctx, primary)
	if err != nil {
		return err
	}
	plan, err := a.detachReplica(replicaCtx, primaryCtx, databases)
	if err != nil {
		return err
	}
	if err := s.runReplicaPlan(ctx, primary, replica, plan); err != nil {
		return err
	}
	// The replica's own record is cleared of its primary first so the destroy
	// path cannot mistake it for an instance that still has followers.
	replica.PrimaryID = ""
	replica.Role = RolePrimary
	if err := s.store.UpdateInstance(ctx, replica); err != nil {
		return err
	}
	if err := s.Destroy(ctx, DestroyRequest{
		TenantID:   replica.TenantID,
		InstanceID: replica.ID,
		Confirm:    true,
		Actor:      req.Actor,
	}); err != nil {
		return err
	}
	s.audit(req.Actor, "database.replica.removed", primary,
		slog.String("replica_id", replica.ID), slog.String("replica", replica.Name))
	return nil
}

// storeAdminCredential records an administrative password that was generated
// elsewhere, used when a replica shares its primary's account.
func (s *Service) storeAdminCredential(ctx context.Context, inst *Instance, password string) error {
	sealed, err := s.encrypt(password)
	if err != nil {
		return err
	}
	return s.store.CreateCredential(ctx, &Credential{
		ID:         s.newID(),
		TenantID:   inst.TenantID,
		InstanceID: inst.ID,
		Username:   inst.AdminUser,
		Role:       RoleAdmin,
		Secret:     sealed,
		CreatedAt:  s.now().UTC(),
	})
}

// runReplicaPlan executes an ordered replication plan, sending each step to
// the end of the pair the adapter named.
func (s *Service) runReplicaPlan(ctx context.Context, primary, replica *Instance, plan []replicaCommand) error {
	for _, step := range plan {
		target := primary
		if step.Target == targetReplica {
			target = replica
		}
		if _, err := s.run(ctx, target, step.Cmd); err != nil {
			return err
		}
	}
	return nil
}

// seedReplica copies each database from the primary into the new replica.
// Engines whose replication stream carries no schema need this before the
// stream is attached; the adapter says which ones through replicaNeedsSeed.
func (s *Service) seedReplica(ctx context.Context, a adapter, primary, replica *Instance,
	primaryCtx, replicaCtx engineCtx, databases []string) error {
	for _, name := range databases {
		if err := s.createDatabaseIn(ctx, a, replicaCtx, replica, name, replica.AdminUser); err != nil {
			return err
		}
		if err := s.copyDatabase(ctx, a, primary, replica, primaryCtx, replicaCtx, name); err != nil {
			return err
		}
	}
	return nil
}

// copyDatabase streams one database out of the primary and into the replica
// without ever staging it on the cashp host: the dump is piped straight from
// one container's standard output into the other's standard input.
func (s *Service) copyDatabase(ctx context.Context, a adapter, primary, replica *Instance,
	primaryCtx, replicaCtx engineCtx, name string) error {
	pr, pw := io.Pipe()
	dumpCmds, err := a.dump(primaryCtx, name, pw)
	if err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return err
	}
	plan, err := a.restore(replicaCtx, name, pr)
	if err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return err
	}
	if plan.Online == nil {
		_ = pw.Close()
		_ = pr.Close()
		return ErrUnsupported(primary.Engine, "seeding a replica")
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := s.runAll(ctx, primary, dumpCmds)
		done <- runErr
		_ = pw.CloseWithError(runErr)
	}()
	_, restoreErr := s.run(ctx, replica, *plan.Online)
	_ = pr.Close()
	if dumpErr := <-done; dumpErr != nil {
		return dumpErr
	}
	return restoreErr
}
