package admin

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/logging"
	"github.com/webappsgo/cashp/src/notify"
	"github.com/webappsgo/cashp/src/security"
)

// adminRecord is one row of the admins table. The TOTP secret is held
// encrypted and is only unwrapped for the duration of a verification.
type adminRecord struct {
	ID                string
	Username          string
	PasswordHash      string
	AccountEmail      string
	NotificationEmail string
	IsPrimary         bool
	Disabled          bool
	Source            string
	ExternalID        string
	Groups            string
	TOTPSecret        string
	TOTPEnabled       bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LastLoginAt       time.Time
}

// sessionRecord is a live admin session.
type sessionRecord struct {
	AdminID   string
	Kind      string
	ExpiresAt time.Time
}

// auditRecord is one entry of the queryable audit trail.
type auditRecord struct {
	OccurredAt time.Time
	Category   string
	Event      string
	Actor      string
	Target     string
	Detail     string
}

// inviteRecord is a pending invite for an additional server admin.
type inviteRecord struct {
	ID        string
	Username  string
	CreatedBy string
	CreatedAt time.Time
	ExpiresAt time.Time
	MaxUses   int
	Uses      int
}

// tokenRecord is an API token belonging to an admin account.
type tokenRecord struct {
	ID            string
	Name          string
	DisplayPrefix string
	CreatedAt     time.Time
	LastUsedAt    time.Time
}

// Session and invite lifetimes from AI.md PART 17.
const (
	// sessionTTL is the default admin session duration.
	sessionTTL = 30 * 24 * time.Hour
	// rememberTTL extends a session when "remember me" is ticked.
	rememberTTL = 90 * 24 * time.Hour
	// setupSessionTTL bounds how long a redeemed setup token stays usable.
	setupSessionTTL = time.Hour
	// setupTokenTTL bounds how long an unredeemed bootstrap token stays valid.
	setupTokenTTL = 24 * time.Hour
	// mfaSessionTTL bounds the window between password and TOTP entry.
	mfaSessionTTL = 5 * time.Minute
)

// Session kinds distinguish a fully authenticated session from the short-lived
// intermediate states used by the setup wizard and the second auth factor.
const (
	sessionKindActive  = "session"
	sessionKindSetup   = "setup"
	sessionKindPending = "pending_mfa"
)

// totpKeyName is the row name of the wrapping key in admin_secrets.
const totpKeyName = "totp_wrap_key"

// errNoRow is returned by the lookup helpers when nothing matched.
var errNoRow = errors.New("admin: no matching record")

// newID returns a random 128-bit identifier as lowercase hex.
func newID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("admin: generate id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// unix converts a stored Unix second count into a time, mapping zero onto the
// zero time so templates can render "never".
func unix(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

// countAdmins returns how many admin accounts exist.
func (p *Panel) countAdmins(ctx context.Context) (int, error) {
	var count int
	row := p.db.QueryRowContext(ctx, database.TimeoutSelect, `SELECT COUNT(*) FROM admins`)
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// adminColumns is the explicit projection used by every admin lookup. The
// codebase never issues SELECT *.
const adminColumns = `id, username, password_hash, account_email, notification_email,
	is_primary, disabled, source, external_id, group_names, totp_secret, totp_enabled,
	created_at, updated_at, last_login_at`

// scanAdmin materialises one admins row from a scanner.
func scanAdmin(scan func(dest ...any) error) (*adminRecord, error) {
	var (
		rec                                            adminRecord
		isPrimary, disabled, totpEnabled               int64
		createdAt, updatedAt, lastLoginAt              int64
		email, notifyEmail, external, groups, totpSecr sql.NullString
	)
	err := scan(&rec.ID, &rec.Username, &rec.PasswordHash, &email, &notifyEmail,
		&isPrimary, &disabled, &rec.Source, &external, &groups, &totpSecr, &totpEnabled,
		&createdAt, &updatedAt, &lastLoginAt)
	if err != nil {
		return nil, err
	}
	rec.AccountEmail = email.String
	rec.NotificationEmail = notifyEmail.String
	rec.ExternalID = external.String
	rec.Groups = groups.String
	rec.TOTPSecret = totpSecr.String
	rec.IsPrimary = isPrimary != 0
	rec.Disabled = disabled != 0
	rec.TOTPEnabled = totpEnabled != 0
	rec.CreatedAt = unix(createdAt)
	rec.UpdatedAt = unix(updatedAt)
	rec.LastLoginAt = unix(lastLoginAt)
	return &rec, nil
}

// adminByUsername loads one admin by its unique username.
func (p *Panel) adminByUsername(ctx context.Context, username string) (*adminRecord, error) {
	row := p.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+adminColumns+` FROM admins WHERE username = ?`, username)
	rec, err := scanAdmin(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoRow
	}
	return rec, err
}

// adminByID loads one admin by its identifier.
func (p *Panel) adminByID(ctx context.Context, id string) (*adminRecord, error) {
	row := p.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+adminColumns+` FROM admins WHERE id = ?`, id)
	rec, err := scanAdmin(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoRow
	}
	return rec, err
}

// createAdmin inserts a new admin account with an already-hashed password.
func (p *Panel) createAdmin(ctx context.Context, username, passwordHash, email string, primary bool) (*adminRecord, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	primaryFlag := 0
	if primary {
		primaryFlag = 1
	}
	_, err = p.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO admins (id, username, password_hash, account_email, notification_email,
			is_primary, disabled, source, external_id, group_names, totp_secret, totp_enabled,
			created_at, updated_at, last_login_at, last_sync_at)
		 VALUES (?, ?, ?, ?, '', ?, 0, 'local', '', '', '', 0, ?, ?, 0, 0)`,
		id, username, passwordHash, email, primaryFlag, now, now)
	if err != nil {
		return nil, err
	}
	return p.adminByID(ctx, id)
}

// updateAdminPassword replaces the stored password hash.
func (p *Panel) updateAdminPassword(ctx context.Context, id, passwordHash string) error {
	_, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE admins SET password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, time.Now().Unix(), id)
	return err
}

// updateAdminEmails stores the account and notification addresses.
func (p *Panel) updateAdminEmails(ctx context.Context, id, account, notify string) error {
	_, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE admins SET account_email = ?, notification_email = ?, updated_at = ? WHERE id = ?`,
		account, notify, time.Now().Unix(), id)
	return err
}

// setAdminDisabled enables or disables an account. The primary admin is
// protected by the caller; this helper never relaxes that check itself.
func (p *Panel) setAdminDisabled(ctx context.Context, username string, disabled bool) (int64, error) {
	flag := 0
	if disabled {
		flag = 1
	}
	res, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE admins SET disabled = ?, updated_at = ? WHERE username = ? AND is_primary = 0`,
		flag, time.Now().Unix(), username)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// deleteAdminByUsername removes a non-primary admin account and everything
// attached to it. The is_primary guard lives in the statement itself so the
// primary account can never be removed through the panel.
func (p *Panel) deleteAdminByUsername(ctx context.Context, username string) (int64, error) {
	target, err := p.adminByUsername(ctx, username)
	if errors.Is(err, errNoRow) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	res, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`DELETE FROM admins WHERE username = ? AND is_primary = 0`, username)
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil || affected == 0 {
		return affected, err
	}
	for _, stmt := range []string{
		`DELETE FROM admin_sessions WHERE admin_id = ?`,
		`DELETE FROM admin_api_tokens WHERE admin_id = ?`,
		`DELETE FROM admin_recovery_codes WHERE admin_id = ?`,
		`DELETE FROM admin_preferences WHERE admin_id = ?`,
	} {
		if _, err := p.db.ExecContext(ctx, database.TimeoutWrite, stmt, target.ID); err != nil {
			return affected, err
		}
	}
	return affected, nil
}

// createSession stores a session and returns the plaintext cookie value.
func (p *Panel) createSession(ctx context.Context, adminID, kind string, ttl time.Duration) (string, error) {
	plaintext, err := newID()
	if err != nil {
		return "", err
	}
	// A second random half keeps the cookie value at 256 bits of entropy.
	suffix, err := newID()
	if err != nil {
		return "", err
	}
	plaintext += suffix
	now := time.Now()
	_, err = p.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO admin_sessions (token_hash, admin_id, kind, created_at, expires_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		security.HashToken(plaintext), adminID, kind, now.Unix(), now.Add(ttl).Unix(), now.Unix())
	if err != nil {
		return "", err
	}
	return plaintext, nil
}

// lookupSession resolves a cookie value to a live session, refreshing the
// last-seen stamp so the online-admin list stays accurate.
func (p *Panel) lookupSession(ctx context.Context, plaintext string) (*sessionRecord, error) {
	if plaintext == "" {
		return nil, errNoRow
	}
	hash := security.HashToken(plaintext)
	var (
		rec       sessionRecord
		expiresAt int64
	)
	row := p.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT admin_id, kind, expires_at FROM admin_sessions WHERE token_hash = ?`, hash)
	if err := row.Scan(&rec.AdminID, &rec.Kind, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errNoRow
		}
		return nil, err
	}
	rec.ExpiresAt = unix(expiresAt)
	if !rec.ExpiresAt.After(time.Now()) {
		return nil, errNoRow
	}
	if _, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE admin_sessions SET last_seen_at = ? WHERE token_hash = ?`,
		time.Now().Unix(), hash); err != nil {
		return nil, err
	}
	return &rec, nil
}

// deleteSession revokes a single session.
func (p *Panel) deleteSession(ctx context.Context, plaintext string) error {
	if plaintext == "" {
		return nil
	}
	_, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`DELETE FROM admin_sessions WHERE token_hash = ?`, security.HashToken(plaintext))
	return err
}

// deleteAdminSessions revokes every session of one admin, used after a
// password change so other devices are signed out.
func (p *Panel) deleteAdminSessions(ctx context.Context, adminID string) error {
	_, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`DELETE FROM admin_sessions WHERE admin_id = ?`, adminID)
	return err
}

// onlineAdmins returns the usernames of admins seen within the window. Only
// the username is exposed: PART 17 forbids showing peer session detail.
func (p *Panel) onlineAdmins(ctx context.Context, window time.Duration) ([]string, error) {
	cutoff := time.Now().Add(-window).Unix()
	rows, err := p.db.QueryContext(ctx, database.TimeoutJoin,
		`SELECT DISTINCT a.username FROM admin_sessions s
		 JOIN admins a ON a.id = s.admin_id
		 WHERE s.kind = ? AND s.last_seen_at >= ? AND s.expires_at > ?
		 ORDER BY a.username`,
		sessionKindActive, cutoff, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// purgeExpiredSessions removes sessions whose deadline has passed.
func (p *Panel) purgeExpiredSessions(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`DELETE FROM admin_sessions WHERE expires_at <= ?`, time.Now().Unix())
	return err
}

// setting returns a stored server setting.
func (p *Panel) setting(ctx context.Context, key string) (string, bool, error) {
	var value string
	row := p.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT setting_value FROM admin_settings WHERE setting_key = ?`, key)
	if err := row.Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

// Setting exposes one stored server setting to the rest of the application so
// runtime components can honour what an administrator configured.
func (p *Panel) Setting(ctx context.Context, key string) (string, bool, error) {
	return p.setting(ctx, key)
}

// settingsWithPrefix loads every stored setting whose key starts with prefix.
func (p *Panel) settingsWithPrefix(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := p.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT setting_key, setting_value FROM admin_settings WHERE setting_key LIKE ?`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

// putSetting stores or replaces a server setting.
func (p *Panel) putSetting(ctx context.Context, key, value, actor string) error {
	now := time.Now().Unix()
	res, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE admin_settings SET setting_value = ?, updated_at = ?, updated_by = ? WHERE setting_key = ?`,
		value, now, actor, key)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	_, err = p.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO admin_settings (setting_key, setting_value, updated_at, updated_by) VALUES (?, ?, ?, ?)`,
		key, value, now, actor)
	return err
}

// preference returns one stored preference for an admin.
func (p *Panel) preference(ctx context.Context, adminID, key string) (string, bool, error) {
	var value string
	row := p.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT pref_value FROM admin_preferences WHERE pref_id = ?`, adminID+":"+key)
	if err := row.Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

// preferences loads every preference of one admin.
func (p *Panel) preferences(ctx context.Context, adminID string) (map[string]string, error) {
	rows, err := p.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT pref_key, pref_value FROM admin_preferences WHERE admin_id = ?`, adminID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

// putPreference stores or replaces one preference of one admin.
func (p *Panel) putPreference(ctx context.Context, adminID, key, value string) error {
	now := time.Now().Unix()
	res, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE admin_preferences SET pref_value = ?, updated_at = ? WHERE pref_id = ?`,
		value, now, adminID+":"+key)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	_, err = p.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO admin_preferences (pref_id, admin_id, pref_key, pref_value, updated_at) VALUES (?, ?, ?, ?, ?)`,
		adminID+":"+key, adminID, key, value, now)
	return err
}

// recordAudit appends an entry to both the append-only audit log and the
// queryable table the panel reads. Values are already redaction-safe: callers
// never pass secrets in.
func (p *Panel) recordAudit(ctx context.Context, category, event, actor, target, detail string) {
	logging.Audit().Info(event,
		"category", category,
		"actor", actor,
		"target", target,
		"detail", detail)

	id, err := newID()
	if err != nil {
		return
	}
	_, _ = p.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO admin_audit (id, occurred_at, category, event, actor, target, detail)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, time.Now().Unix(), category, event, actor, target, detail)
}

// notify dispatches one admin-panel event, tolerating both an absent
// notifier and a delivery failure - a notification is never allowed to fail
// the sign-in/sign-out flow it describes.
func (p *Panel) notify(ctx context.Context, event string, vars map[string]string) {
	if p.notifier == nil {
		return
	}
	if err := p.notifier.Notify(ctx, notify.Message{Event: event, Vars: vars}); err != nil {
		logging.L().Warn("admin notification failed", "event", event, "error", err.Error())
	}
}

// recentAudit returns the newest audit entries, optionally filtered by
// category and a case-insensitive substring of the event or target.
func (p *Panel) recentAudit(ctx context.Context, category, search string, limit, offset int) ([]auditRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	query := `SELECT occurred_at, category, event, actor, target, detail FROM admin_audit WHERE 1 = 1`
	args := []any{}
	if category != "" && category != "all" {
		query += ` AND category = ?`
		args = append(args, category)
	}
	if search != "" {
		query += ` AND (LOWER(event) LIKE ? OR LOWER(target) LIKE ? OR LOWER(actor) LIKE ?)`
		needle := "%" + strings.ToLower(search) + "%"
		args = append(args, needle, needle, needle)
	}
	query += ` ORDER BY occurred_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := p.db.QueryContext(ctx, database.TimeoutSelect, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []auditRecord
	for rows.Next() {
		var (
			rec        auditRecord
			occurredAt int64
		)
		if err := rows.Scan(&occurredAt, &rec.Category, &rec.Event, &rec.Actor, &rec.Target, &rec.Detail); err != nil {
			return nil, err
		}
		rec.OccurredAt = unix(occurredAt)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// auditCategories lists the categories present in the store, so the log filter
// never offers a value that would return an empty page.
func (p *Panel) auditCategories(ctx context.Context) ([]string, error) {
	rows, err := p.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT DISTINCT category FROM admin_audit ORDER BY category`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

// createInvite stores a single-use (by default) admin invite and returns the
// plaintext token, which is shown to the inviting admin exactly once.
func (p *Panel) createInvite(ctx context.Context, username, createdBy string, ttl time.Duration, maxUses int) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	plaintext, hash, err := security.GenerateToken(security.PrefixAdmin)
	if err != nil {
		return "", err
	}
	now := time.Now()
	_, err = p.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO admin_invites (id, username, token_hash, created_by, created_at, expires_at, max_uses, uses, revoked_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0)`,
		id, username, hash, createdBy, now.Unix(), now.Add(ttl).Unix(), maxUses)
	if err != nil {
		return "", err
	}
	return plaintext, nil
}

// pendingInvites lists invites that are still redeemable.
func (p *Panel) pendingInvites(ctx context.Context) ([]inviteRecord, error) {
	rows, err := p.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT id, username, created_by, created_at, expires_at, max_uses, uses
		 FROM admin_invites WHERE revoked_at = 0 AND expires_at > ? ORDER BY created_at DESC`,
		time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []inviteRecord
	for rows.Next() {
		var (
			rec                  inviteRecord
			createdAt, expiresAt int64
		)
		if err := rows.Scan(&rec.ID, &rec.Username, &rec.CreatedBy, &createdAt, &expiresAt, &rec.MaxUses, &rec.Uses); err != nil {
			return nil, err
		}
		rec.CreatedAt = unix(createdAt)
		rec.ExpiresAt = unix(expiresAt)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// revokeInvite marks an invite unusable.
func (p *Panel) revokeInvite(ctx context.Context, id string) (int64, error) {
	res, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE admin_invites SET revoked_at = ? WHERE id = ? AND revoked_at = 0`,
		time.Now().Unix(), id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// createAPIToken issues an API token for an admin and returns the plaintext,
// which is displayed once and never stored.
func (p *Panel) createAPIToken(ctx context.Context, adminID, name string) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	plaintext, hash, err := security.GenerateToken(security.PrefixAdmin)
	if err != nil {
		return "", err
	}
	_, err = p.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO admin_api_tokens (id, admin_id, name, token_hash, display_prefix, created_at, last_used_at, revoked_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, 0)`,
		id, adminID, name, hash, security.TokenDisplayPrefix(plaintext), time.Now().Unix())
	if err != nil {
		return "", err
	}
	return plaintext, nil
}

// apiTokens lists an admin's own live tokens. Tokens of other admins are never
// visible: every query is scoped by admin_id.
func (p *Panel) apiTokens(ctx context.Context, adminID string) ([]tokenRecord, error) {
	rows, err := p.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT id, name, display_prefix, created_at, last_used_at
		 FROM admin_api_tokens WHERE admin_id = ? AND revoked_at = 0 ORDER BY created_at DESC`, adminID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []tokenRecord
	for rows.Next() {
		var (
			rec                   tokenRecord
			createdAt, lastUsedAt int64
		)
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.DisplayPrefix, &createdAt, &lastUsedAt); err != nil {
			return nil, err
		}
		rec.CreatedAt = unix(createdAt)
		rec.LastUsedAt = unix(lastUsedAt)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// revokeAPIToken revokes one of the calling admin's own tokens.
func (p *Panel) revokeAPIToken(ctx context.Context, adminID, id string) (int64, error) {
	res, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE admin_api_tokens SET revoked_at = ? WHERE id = ? AND admin_id = ? AND revoked_at = 0`,
		time.Now().Unix(), id, adminID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// wrapKey returns the AES key used to encrypt TOTP secrets, creating it on
// first use. Storing the key beside the ciphertext protects against database
// dumps taken through a read-only channel, not against full host compromise.
func (p *Panel) wrapKey(ctx context.Context) ([]byte, error) {
	var encoded string
	row := p.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT secret_value FROM admin_secrets WHERE name = ?`, totpKeyName)
	err := row.Scan(&encoded)
	switch {
	case err == nil:
		return hex.DecodeString(encoded)
	case !errors.Is(err, sql.ErrNoRows):
		return nil, err
	}

	key, err := security.RandomSecret(security.SecretLen)
	if err != nil {
		return nil, err
	}
	if _, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO admin_secrets (name, secret_value, created_at) VALUES (?, ?, ?)`,
		totpKeyName, hex.EncodeToString(key), time.Now().Unix()); err != nil {
		return nil, err
	}
	return key, nil
}

// storeTOTPSecret encrypts and saves an admin's TOTP secret.
func (p *Panel) storeTOTPSecret(ctx context.Context, adminID, secret string, enabled bool) error {
	key, err := p.wrapKey(ctx)
	if err != nil {
		return err
	}
	sealed := ""
	if secret != "" {
		ciphertext, err := security.Encrypt(key, []byte(secret))
		if err != nil {
			return err
		}
		sealed = hex.EncodeToString(ciphertext)
	}
	flag := 0
	if enabled {
		flag = 1
	}
	_, err = p.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE admins SET totp_secret = ?, totp_enabled = ?, updated_at = ? WHERE id = ?`,
		sealed, flag, time.Now().Unix(), adminID)
	return err
}

// loadTOTPSecret decrypts an admin's stored TOTP secret.
func (p *Panel) loadTOTPSecret(ctx context.Context, rec *adminRecord) (string, error) {
	if rec == nil || rec.TOTPSecret == "" {
		return "", nil
	}
	key, err := p.wrapKey(ctx)
	if err != nil {
		return "", err
	}
	ciphertext, err := hex.DecodeString(rec.TOTPSecret)
	if err != nil {
		return "", err
	}
	plaintext, err := security.Decrypt(key, ciphertext)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// storeRecoveryCodes replaces an admin's recovery codes with fresh hashes.
func (p *Panel) storeRecoveryCodes(ctx context.Context, adminID string, codes []string) error {
	if _, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`DELETE FROM admin_recovery_codes WHERE admin_id = ?`, adminID); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, code := range codes {
		id, err := newID()
		if err != nil {
			return err
		}
		if _, err := p.db.ExecContext(ctx, database.TimeoutWrite,
			`INSERT INTO admin_recovery_codes (id, admin_id, code_hash, created_at, used_at) VALUES (?, ?, ?, ?, 0)`,
			id, adminID, security.HashToken(code), now); err != nil {
			return err
		}
	}
	return nil
}

// consumeRecoveryCode marks a recovery code used and reports whether it was
// valid. The update is the check, so a code can never be redeemed twice.
func (p *Panel) consumeRecoveryCode(ctx context.Context, adminID, code string) (bool, error) {
	res, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE admin_recovery_codes SET used_at = ? WHERE admin_id = ? AND code_hash = ? AND used_at = 0`,
		time.Now().Unix(), adminID, security.HashToken(code))
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// countRecoveryCodes returns how many unused recovery codes remain.
func (p *Panel) countRecoveryCodes(ctx context.Context, adminID string) (int, error) {
	var count int
	row := p.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT COUNT(*) FROM admin_recovery_codes WHERE admin_id = ? AND used_at = 0`, adminID)
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// revokeAllSessions signs every administrator out, including the caller.
func (p *Panel) revokeAllSessions(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, database.TimeoutBulk, `DELETE FROM admin_sessions WHERE kind = ?`, sessionKindActive)
	return err
}

// nodeRecord is one enrolled cluster node.
type nodeRecord struct {
	ID         string
	Name       string
	Address    string
	Port       int
	Status     string
	Labels     string
	CreatedAt  time.Time
	CreatedBy  string
	LastSeenAt time.Time
}

// nodeColumns is the explicit projection used for every node read.
const nodeColumns = `id, name, address, port, status, labels, created_at, created_by, last_seen_at`

// clusterNodes lists the enrolled nodes, newest first.
func (p *Panel) clusterNodes(ctx context.Context) ([]nodeRecord, error) {
	rows, err := p.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+nodeColumns+` FROM admin_cluster_nodes ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	nodes := make([]nodeRecord, 0, 8)
	for rows.Next() {
		var rec nodeRecord
		var created, seen int64
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.Address, &rec.Port, &rec.Status, &rec.Labels,
			&created, &rec.CreatedBy, &seen); err != nil {
			return nil, err
		}
		rec.CreatedAt = unix(created)
		rec.LastSeenAt = unix(seen)
		nodes = append(nodes, rec)
	}
	return nodes, rows.Err()
}

// createClusterNode enrols a node and returns the one-time join token the agent
// authenticates with. Only the token's hash is stored.
func (p *Panel) createClusterNode(ctx context.Context, name, address string, port int, labels, createdBy string) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	plaintext, hash, err := security.GenerateToken(security.PrefixAdminAgent)
	if err != nil {
		return "", err
	}
	if _, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO admin_cluster_nodes (id, name, address, port, join_token_hash, status, labels, created_at, created_by, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, 'pending', ?, ?, ?, 0)`,
		id, name, address, port, hash, labels, time.Now().Unix(), createdBy); err != nil {
		return "", err
	}
	return plaintext, nil
}

// deleteClusterNode removes a node from the cluster.
func (p *Panel) deleteClusterNode(ctx context.Context, id string) (int64, error) {
	res, err := p.db.ExecContext(ctx, database.TimeoutWrite, `DELETE FROM admin_cluster_nodes WHERE id = ?`, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
