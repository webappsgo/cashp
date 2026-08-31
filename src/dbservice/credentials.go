package dbservice

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"strconv"

	"github.com/webappsgo/cashp/src/security"
)

// Credential handling for managed instances. Three rules hold everywhere in
// this file: a password is generated with security.RandomSecret, it is stored
// only as security.Encrypt ciphertext, and it appears in the clear exactly
// once, in the UserCredential handed back to the owning tenant at issuance or
// rotation. Nothing here writes a password, a DSN or a command line to a log.

// encrypt seals a generated password for storage.
func (s *Service) encrypt(plaintext string) ([]byte, error) {
	sealed, err := security.Encrypt(s.key, []byte(plaintext))
	if err != nil {
		return nil, ErrInternal(err, "That credential could not be stored.")
	}
	return sealed, nil
}

// decrypt opens a stored password for the length of one operation.
func (s *Service) decrypt(sealed []byte) (string, error) {
	if len(sealed) == 0 {
		return "", ErrInternal(nil, "That credential could not be read.")
	}
	plain, err := security.Decrypt(s.key, sealed)
	if err != nil {
		return "", ErrInternal(err, "That credential could not be read.")
	}
	return string(plain), nil
}

// issueAdminCredential generates and stores the instance's administrative
// account. That account belongs to cashp: it is never returned to a tenant and
// never appears in a connection string handed out by this package.
func (s *Service) issueAdminCredential(ctx context.Context, inst *Instance) (string, error) {
	password, err := generatePassword(passwordLen)
	if err != nil {
		return "", err
	}
	sealed, err := s.encrypt(password)
	if err != nil {
		return "", err
	}
	now := s.now().UTC()
	cred := &Credential{
		ID:         s.newID(),
		TenantID:   inst.TenantID,
		InstanceID: inst.ID,
		Username:   inst.AdminUser,
		Role:       RoleAdmin,
		Secret:     sealed,
		CreatedAt:  now,
	}
	if err := s.store.CreateCredential(ctx, cred); err != nil {
		return "", err
	}
	return password, nil
}

// CreateUser issues a least-privilege account inside an instance and returns
// its credential exactly once. The account is created with no privileges at
// all and then granted only the requested level on the one database it is
// scoped to.
func (s *Service) CreateUser(ctx context.Context, req CreateUserRequest) (*UserCredential, error) {
	inst, err := s.running(ctx, req.TenantID, req.InstanceID)
	if err != nil {
		return nil, err
	}
	a, c, err := s.engineContext(ctx, inst)
	if err != nil {
		return nil, err
	}
	if !a.capabilities().Users {
		return nil, ErrUnsupported(inst.Engine, "user accounts")
	}
	if err := ValidateIdentifier(inst.Engine, "username", req.Username); err != nil {
		return nil, err
	}
	if req.Username == inst.AdminUser {
		return nil, ErrConflict("That account name is reserved.")
	}
	level := req.Grant
	if level == "" {
		level = GrantReadWrite
	}
	if !level.Valid() {
		return nil, ErrValidation("That privilege level is not one this server issues.")
	}
	database, err := s.resolveDatabase(ctx, a, inst, req.Database)
	if err != nil {
		return nil, err
	}
	if existing, err := s.store.GetCredential(ctx, inst.TenantID, inst.ID, req.Username); err == nil && existing != nil {
		return nil, ErrConflict("That account already exists on this instance.")
	} else if err != nil && !IsNotFound(err) {
		return nil, err
	}

	password, err := generatePassword(passwordLen)
	if err != nil {
		return nil, err
	}
	create, err := a.createUser(c, req.Username, database, password)
	if err != nil {
		return nil, err
	}
	grants, err := a.grant(c, req.Username, database, level)
	if err != nil {
		return nil, err
	}
	if _, err := s.runAll(ctx, inst, append(create, grants...)); err != nil {
		return nil, err
	}
	sealed, err := s.encrypt(password)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	cred := &Credential{
		ID:         s.newID(),
		TenantID:   inst.TenantID,
		InstanceID: inst.ID,
		Username:   req.Username,
		Role:       RoleApp,
		Database:   database,
		Grant:      level,
		Secret:     sealed,
		CreatedAt:  now,
	}
	if err := s.store.CreateCredential(ctx, cred); err != nil {
		return nil, err
	}
	s.audit(req.Actor, "database.user.created", inst,
		slog.String("username", req.Username),
		slog.String("database", database),
		slog.String("grant", string(level)))
	return s.userCredential(a, inst, cred, password), nil
}

// RotateUser replaces one account's password inside the engine and in the
// store, returning the new credential exactly once.
func (s *Service) RotateUser(ctx context.Context, tenantID, instanceID, username, actor string) (*UserCredential, error) {
	inst, err := s.running(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	cred, err := s.store.GetCredential(ctx, tenantID, instanceID, username)
	if err != nil {
		return nil, err
	}
	if cred.Role == RoleAdmin {
		return nil, ErrForbidden("The administrative account of a managed instance cannot be rotated by a tenant.")
	}
	a, c, err := s.engineContext(ctx, inst)
	if err != nil {
		return nil, err
	}
	password, err := generatePassword(passwordLen)
	if err != nil {
		return nil, err
	}
	cmds, err := a.setPassword(c, cred.Username, cred.Database, password)
	if err != nil {
		return nil, err
	}
	if _, err := s.runAll(ctx, inst, cmds); err != nil {
		return nil, err
	}
	sealed, err := s.encrypt(password)
	if err != nil {
		return nil, err
	}
	cred.Secret = sealed
	cred.RotatedAt = s.now().UTC()
	if err := s.store.UpdateCredential(ctx, cred); err != nil {
		return nil, err
	}
	s.audit(actor, "database.user.rotated", inst, slog.String("username", cred.Username))
	return s.userCredential(a, inst, cred, password), nil
}

// DropUser removes an account from the instance and revokes its record.
func (s *Service) DropUser(ctx context.Context, req DropRequest) error {
	inst, err := s.running(ctx, req.TenantID, req.InstanceID)
	if err != nil {
		return err
	}
	cred, err := s.store.GetCredential(ctx, req.TenantID, req.InstanceID, req.Name)
	if err != nil {
		return err
	}
	if cred.Role == RoleAdmin {
		return ErrForbidden("The administrative account of a managed instance cannot be removed.")
	}
	a, c, err := s.engineContext(ctx, inst)
	if err != nil {
		return err
	}
	cmds, err := a.dropUser(c, cred.Username, cred.Database)
	if err != nil {
		return err
	}
	if _, err := s.runAll(ctx, inst, cmds); err != nil {
		return err
	}
	cred.RevokedAt = s.now().UTC()
	if err := s.store.UpdateCredential(ctx, cred); err != nil {
		return err
	}
	s.audit(req.Actor, "database.user.dropped", inst, slog.String("username", cred.Username))
	return nil
}

// Grant changes one account's privilege level on one database.
func (s *Service) Grant(ctx context.Context, req GrantRequest) error {
	inst, err := s.running(ctx, req.TenantID, req.InstanceID)
	if err != nil {
		return err
	}
	a, c, err := s.engineContext(ctx, inst)
	if err != nil {
		return err
	}
	if !a.capabilities().Grants {
		return ErrUnsupported(inst.Engine, "per-database privileges")
	}
	cred, err := s.store.GetCredential(ctx, req.TenantID, req.InstanceID, req.Username)
	if err != nil {
		return err
	}
	if cred.Role == RoleAdmin {
		return ErrForbidden("The administrative account of a managed instance cannot be regranted.")
	}
	if !req.Grant.Valid() {
		return ErrValidation("That privilege level is not one this server issues.")
	}
	database, err := s.resolveDatabase(ctx, a, inst, req.Database)
	if err != nil {
		return err
	}
	cmds, err := a.grant(c, cred.Username, database, req.Grant)
	if err != nil {
		return err
	}
	if _, err := s.runAll(ctx, inst, cmds); err != nil {
		return err
	}
	cred.Database = database
	cred.Grant = req.Grant
	if err := s.store.UpdateCredential(ctx, cred); err != nil {
		return err
	}
	s.audit(req.Actor, "database.user.granted", inst,
		slog.String("username", cred.Username),
		slog.String("database", database),
		slog.String("grant", string(req.Grant)))
	return nil
}

// Revoke removes one account's access to one database while leaving the
// account itself in place.
func (s *Service) Revoke(ctx context.Context, req GrantRequest) error {
	inst, err := s.running(ctx, req.TenantID, req.InstanceID)
	if err != nil {
		return err
	}
	a, c, err := s.engineContext(ctx, inst)
	if err != nil {
		return err
	}
	if !a.capabilities().Grants {
		return ErrUnsupported(inst.Engine, "per-database privileges")
	}
	cred, err := s.store.GetCredential(ctx, req.TenantID, req.InstanceID, req.Username)
	if err != nil {
		return err
	}
	if cred.Role == RoleAdmin {
		return ErrForbidden("The administrative account of a managed instance cannot be revoked.")
	}
	database, err := s.resolveDatabase(ctx, a, inst, req.Database)
	if err != nil {
		return err
	}
	cmds, err := a.revoke(c, cred.Username, database)
	if err != nil {
		return err
	}
	if _, err := s.runAll(ctx, inst, cmds); err != nil {
		return err
	}
	cred.Grant = ""
	if err := s.store.UpdateCredential(ctx, cred); err != nil {
		return err
	}
	s.audit(req.Actor, "database.user.revoked", inst,
		slog.String("username", cred.Username),
		slog.String("database", database))
	return nil
}

// ListUsers returns an instance's tenant-facing accounts. The administrative
// account is excluded because it is not a tenant's to see or use.
func (s *Service) ListUsers(ctx context.Context, tenantID, instanceID string) ([]*Credential, error) {
	if _, err := s.live(ctx, tenantID, instanceID); err != nil {
		return nil, err
	}
	all, err := s.store.ListCredentials(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	out := make([]*Credential, 0, len(all))
	for _, cred := range all {
		if cred.Role == RoleAdmin {
			continue
		}
		out = append(out, cred)
	}
	return out, nil
}

// Connection returns the masked connection descriptor for one account. Both
// the password field and the DSN carry security.MaskedValue, so this payload
// is safe to render in an API response, a UI, a screenshot or a support
// transcript.
func (s *Service) Connection(ctx context.Context, tenantID, instanceID, username string) (*ConnectionInfo, error) {
	inst, err := s.live(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	a, err := adapterFor(inst.Engine)
	if err != nil {
		return nil, err
	}
	cred, err := s.store.GetCredential(ctx, tenantID, instanceID, username)
	if err != nil {
		return nil, err
	}
	if cred.Role == RoleAdmin {
		return nil, ErrNotFound("credential")
	}
	return &ConnectionInfo{
		Engine:   inst.Engine,
		Scheme:   a.scheme(),
		Host:     inst.Host,
		Port:     inst.Port,
		Username: cred.Username,
		Password: security.MaskedValue,
		Database: cred.Database,
		DSN:      buildDSN(a, inst, cred.Username, security.MaskedValue, cred.Database),
	}, nil
}

// Reveal returns one account's full connection string. It is reachable only
// through a tenant-scoped lookup, so a tenant can only ever reveal a
// credential of an instance it owns, and the administrative account is never
// revealable at all.
func (s *Service) Reveal(ctx context.Context, tenantID, instanceID, username, actor string) (*UserCredential, error) {
	inst, err := s.live(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	a, err := adapterFor(inst.Engine)
	if err != nil {
		return nil, err
	}
	cred, err := s.store.GetCredential(ctx, tenantID, instanceID, username)
	if err != nil {
		return nil, err
	}
	if cred.Role == RoleAdmin {
		return nil, ErrForbidden("The administrative account of a managed instance is not issued to tenants.")
	}
	password, err := s.decrypt(cred.Secret)
	if err != nil {
		return nil, err
	}
	s.audit(actor, "database.credential.revealed", inst, slog.String("username", cred.Username))
	return s.userCredential(a, inst, cred, password), nil
}

// userCredential assembles the one-time payload handed to the owning tenant.
func (s *Service) userCredential(a adapter, inst *Instance, cred *Credential, password string) *UserCredential {
	return &UserCredential{
		TenantID:   inst.TenantID,
		InstanceID: inst.ID,
		Username:   cred.Username,
		Password:   password,
		Database:   cred.Database,
		Grant:      cred.Grant,
		DSN:        buildDSN(a, inst, cred.Username, password, cred.Database),
	}
}

// revokeAllCredentials tombstones every account of a destroyed instance so no
// stored ciphertext outlives the data it protected.
func (s *Service) revokeAllCredentials(ctx context.Context, inst *Instance) error {
	creds, err := s.store.ListCredentials(ctx, inst.TenantID, inst.ID)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	for _, cred := range creds {
		cred.Secret = nil
		cred.RevokedAt = now
		if err := s.store.UpdateCredential(ctx, cred); err != nil {
			return err
		}
	}
	return nil
}

// buildDSN assembles a connection string with net/url so the username, the
// password and the database name are percent-encoded rather than pasted
// together. Callers pass either the real password or security.MaskedValue.
func buildDSN(a adapter, inst *Instance, username, password, database string) string {
	u := &url.URL{
		Scheme: a.scheme(),
		User:   url.UserPassword(username, password),
		Host:   net.JoinHostPort(inst.Host, strconv.Itoa(inst.Port)),
	}
	if database != "" {
		u.Path = "/" + database
	}
	if a.engine() == EngineMongoDB {
		q := u.Query()
		q.Set("authSource", "admin")
		u.RawQuery = q.Encode()
	}
	return u.String()
}
