package dbservice

import (
	"context"
	"log/slog"
)

// Named-database management inside a managed instance. Every identifier that
// reaches an engine passes ValidateIdentifier first and is quoted with that
// engine's own identifier rules by the adapter: no database name is ever
// concatenated into DDL unchecked.

// resolveDatabase validates a requested database name against the engine's
// capabilities and the tenant's own records. An engine without named
// databases returns an empty name, and refuses a request that named one.
func (s *Service) resolveDatabase(ctx context.Context, a adapter, inst *Instance, name string) (string, error) {
	if !a.capabilities().NamedDatabases {
		if name != "" {
			return "", ErrUnsupported(inst.Engine, "named databases")
		}
		return "", nil
	}
	if name == "" {
		return "", ErrValidation("A database name is required for this engine.")
	}
	if err := ValidateIdentifier(inst.Engine, "database", name); err != nil {
		return "", err
	}
	known, err := s.store.ListDatabases(ctx, inst.TenantID, inst.ID)
	if err != nil {
		return "", err
	}
	for _, db := range known {
		if db.Name == name {
			return name, nil
		}
	}
	return "", ErrNotFound("database")
}

// CreateDatabase creates a named database inside an instance and records it
// against the owning tenant.
func (s *Service) CreateDatabase(ctx context.Context, tenantID, instanceID, name, owner, actor string) (*Database, error) {
	inst, err := s.running(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	a, c, err := s.engineContext(ctx, inst)
	if err != nil {
		return nil, err
	}
	if !a.capabilities().NamedDatabases {
		return nil, ErrUnsupported(inst.Engine, "named databases")
	}
	if err := ValidateIdentifier(inst.Engine, "database", name); err != nil {
		return nil, err
	}
	if owner == "" {
		owner = inst.AdminUser
	}
	if err := ValidateIdentifier(inst.Engine, "username", owner); err != nil {
		return nil, err
	}
	known, err := s.store.ListDatabases(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	for _, db := range known {
		if db.Name == name {
			return nil, ErrConflict("That instance already has a database with that name.")
		}
	}
	if err := s.createDatabaseIn(ctx, a, c, inst, name, owner); err != nil {
		return nil, err
	}
	s.audit(actor, "database.database.created", inst,
		slog.String("database", name), slog.String("owner", owner))
	return s.findDatabase(ctx, inst, name)
}

// createDatabaseIn runs the engine's create-database plan and records the row.
func (s *Service) createDatabaseIn(ctx context.Context, a adapter, c engineCtx, inst *Instance, name, owner string) error {
	cmds, err := a.createDatabase(c, name, owner)
	if err != nil {
		return err
	}
	if _, err := s.runAll(ctx, inst, cmds); err != nil {
		return err
	}
	return s.store.CreateDatabase(ctx, &Database{
		ID:         s.newID(),
		TenantID:   inst.TenantID,
		InstanceID: inst.ID,
		Name:       name,
		Owner:      owner,
		CreatedAt:  s.now().UTC(),
	})
}

// findDatabase re-reads one recorded database so the caller sees the stored
// row rather than a constructed copy.
func (s *Service) findDatabase(ctx context.Context, inst *Instance, name string) (*Database, error) {
	known, err := s.store.ListDatabases(ctx, inst.TenantID, inst.ID)
	if err != nil {
		return nil, err
	}
	for _, db := range known {
		if db.Name == name {
			return db, nil
		}
	}
	return nil, ErrNotFound("database")
}

// DropDatabase deletes a named database and everything in it. Like Destroy it
// requires an explicit confirmation flag and is audit logged, because it is
// irreversible for the tenant.
func (s *Service) DropDatabase(ctx context.Context, req DropRequest) error {
	inst, err := s.running(ctx, req.TenantID, req.InstanceID)
	if err != nil {
		return err
	}
	if !req.Confirm {
		return ErrConfirmationRequired(req.Name)
	}
	a, c, err := s.engineContext(ctx, inst)
	if err != nil {
		return err
	}
	name, err := s.resolveDatabase(ctx, a, inst, req.Name)
	if err != nil {
		return err
	}
	cmds, err := a.dropDatabase(c, name)
	if err != nil {
		return err
	}
	if _, err := s.runAll(ctx, inst, cmds); err != nil {
		return err
	}
	if err := s.store.MarkDatabaseDropped(ctx, inst.TenantID, inst.ID, name, s.now().UTC()); err != nil {
		return err
	}
	s.audit(req.Actor, "database.database.dropped", inst,
		slog.String("database", name), slog.Bool("confirmed", true))
	return nil
}

// ListDatabases returns the databases cashp recorded for one instance.
func (s *Service) ListDatabases(ctx context.Context, tenantID, instanceID string) ([]*Database, error) {
	inst, err := s.live(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	a, err := adapterFor(inst.Engine)
	if err != nil {
		return nil, err
	}
	if !a.capabilities().NamedDatabases {
		return nil, ErrUnsupported(inst.Engine, "named databases")
	}
	return s.store.ListDatabases(ctx, tenantID, instanceID)
}

// engineDatabases asks the engine itself which databases exist. It is used by
// the operations that must act on the real contents of an instance rather than
// on cashp's index of it, such as a whole-instance upgrade or a replica seed.
func (s *Service) engineDatabases(ctx context.Context, a adapter, c engineCtx, inst *Instance) ([]string, error) {
	if !a.capabilities().NamedDatabases {
		return nil, nil
	}
	cmds, err := a.listDatabases(c)
	if err != nil {
		return nil, err
	}
	results, err := s.runAll(ctx, inst, cmds)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	names, err := a.parseDatabaseList(results[len(results)-1])
	if err != nil {
		return nil, err
	}
	return sortedNames(names), nil
}

// databaseNames returns the recorded database names of one instance.
func (s *Service) databaseNames(ctx context.Context, inst *Instance) ([]string, error) {
	known, err := s.store.ListDatabases(ctx, inst.TenantID, inst.ID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(known))
	for _, db := range known {
		names = append(names, db.Name)
	}
	return sortedNames(names), nil
}
