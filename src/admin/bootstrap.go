package admin

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/security"
)

// Setting keys written by the setup wizard. They are read by the rest of the
// application through Panel.Setting.
const (
	settingSetupComplete = "setup.complete"
	settingSetupStep     = "setup.step"
	settingAppName       = "server.app_name"
	settingFQDN          = "server.fqdn"
	settingMode          = "server.mode"
	settingTimezone      = "server.timezone"
	settingMultiUser     = "users.multi_user"
	settingCertSource    = "tls.cert_source"
	settingBackupKeyName = "backup_encryption_password"
)

// setupTokenBytes is the token length in bytes; rendered as 32 hex characters.
const setupTokenBytes = 16

// Names of the transient wizard secrets. They live in the encrypted secret
// store and are deleted the moment they have been displayed once.
const (
	secretSetupAPIToken = "setup_api_token"
	secretSetupTOTP     = "setup_totp_secret"
	secretSetupRecovery = "setup_recovery_codes"
	secretSetupPassword = "setup_generated_password"
)

// Bootstrap prepares first-run access. When no administrator exists and no
// unused setup token is present, it mints one and returns the plaintext so the
// caller can print it to the console exactly once. It returns an empty string
// when the server already has an administrator or a live token.
//
// Only the SHA-256 hash of the token is persisted, the row is consumed
// atomically on redemption, and the primary-admin flag it creates can never be
// changed through the panel or the API.
func (p *Panel) Bootstrap(ctx context.Context) (string, error) {
	p.bootstrapOnce.Lock()
	defer p.bootstrapOnce.Unlock()

	count, err := p.countAdmins(ctx)
	if err != nil {
		return "", err
	}
	if count > 0 {
		return "", nil
	}

	var live int
	row := p.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT COUNT(*) FROM admin_setup_tokens WHERE used_at = 0 AND expires_at > ?`, time.Now().Unix())
	if err := row.Scan(&live); err != nil {
		return "", err
	}
	if live > 0 {
		return "", nil
	}

	buf := make([]byte, setupTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("admin: generate setup token: %w", err)
	}
	plaintext := hex.EncodeToString(buf)

	id, err := newID()
	if err != nil {
		return "", err
	}
	now := time.Now()
	if _, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO admin_setup_tokens (id, token_hash, created_at, expires_at, used_at) VALUES (?, ?, ?, ?, 0)`,
		id, security.HashToken(plaintext), now.Unix(), now.Add(setupTokenTTL).Unix()); err != nil {
		return "", err
	}
	p.recordAudit(ctx, "setup", "setup_token_issued", "system", "", "one-time setup token generated")
	return plaintext, nil
}

// SetupBanner renders the console block that shows a freshly minted setup
// token. The caller prints it once; the token is never written to a log file.
func (p *Panel) SetupBanner(token, baseURL string) string {
	target := baseURL + p.url("config/setup")
	return strings.Join([]string{
		"┌───────────────────────────────────────────────────────────┐",
		"│  SETUP REQUIRED                                           │",
		"├───────────────────────────────────────────────────────────┤",
		"│  Setup Token: " + token,
		"│",
		"│  Open " + target,
		"│  and enter this token to complete setup.",
		"│",
		"│  This token is shown ONCE.",
		"└───────────────────────────────────────────────────────────┘",
	}, "\n")
}

// SetupComplete reports whether the setup wizard has been finished.
func (p *Panel) SetupComplete(ctx context.Context) bool {
	value, _, err := p.setting(ctx, settingSetupComplete)
	return err == nil && value == "true"
}

// ensureSetupToken lazily mints a token when the panel is reached before the
// server printed one, so a first run can always be completed. The banner goes
// to standard error, never to the audit or application log.
func (p *Panel) ensureSetupToken(ctx context.Context) {
	token, err := p.Bootstrap(ctx)
	if err != nil || token == "" {
		return
	}
	fmt.Fprintln(os.Stderr, p.SetupBanner(token, ""))
}

// redeemSetupToken consumes the one-time token. The UPDATE is the check, so two
// concurrent redemptions can never both succeed.
func (p *Panel) redeemSetupToken(ctx context.Context, plaintext string) (bool, error) {
	hash := security.HashToken(strings.ToLower(strings.TrimSpace(plaintext)))
	res, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE admin_setup_tokens SET used_at = ? WHERE token_hash = ? AND used_at = 0 AND expires_at > ?`,
		time.Now().Unix(), hash, time.Now().Unix())
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// setupStep returns the wizard step the server is currently expecting.
func (p *Panel) setupStep(ctx context.Context) int {
	value, ok, err := p.setting(ctx, settingSetupStep)
	if err != nil || !ok {
		return 1
	}
	step, err := strconv.Atoi(value)
	if err != nil || step < 1 || step > 6 {
		return 1
	}
	return step
}

// setSetupStep records wizard progress so a reload resumes where it left off.
func (p *Panel) setSetupStep(ctx context.Context, step int) error {
	return p.putSetting(ctx, settingSetupStep, strconv.Itoa(step), "setup")
}

// setupSession resolves the wizard session created when the token was redeemed.
func (p *Panel) setupSession(req *http.Request) (*sessionRecord, error) {
	return p.currentSession(req, sessionKindSetup)
}

// handleSetup renders the setup wizard: the token gate first, then one step at
// a time. Once setup is complete the route is closed.
func (p *Panel) handleSetup(w http.ResponseWriter, req *http.Request) {
	if p.SetupComplete(req.Context()) {
		p.redirect(w, req, "", "info", "Setup has already been completed.")
		return
	}
	p.ensureSetupToken(req.Context())

	sess, err := p.setupSession(req)
	if err != nil {
		p.renderSetupGate(w, req, "")
		return
	}
	p.renderSetupStep(w, req, sess, p.setupStep(req.Context()), "")
}

// renderSetupGate renders the one-time token prompt.
func (p *Panel) renderSetupGate(w http.ResponseWriter, req *http.Request, formError string) {
	ctx := p.newContext(w, req, nil, "Setup", "Enter the one-time setup token.")
	ctx.PageClass = "panel panel-auth"
	ctx.Data = map[string]any{"Error": formError}
	status := http.StatusOK
	if formError != "" {
		status = http.StatusUnauthorized
	}
	p.renderStatus(w, req, status, "setup-gate", ctx)
}

// renderSetupStep renders one wizard step.
func (p *Panel) renderSetupStep(w http.ResponseWriter, req *http.Request, sess *sessionRecord, step int, formError string) {
	ctx := p.newContext(w, req, nil, "Setup", "Complete the initial server setup.")
	ctx.PageClass = "panel panel-auth panel-setup"

	data := map[string]any{
		"Step":     step,
		"Steps":    []int{1, 2, 3, 4, 5, 6},
		"Error":    formError,
		"Defaults": p.setupDefaults(req.Context()),
	}

	switch step {
	case 2:
		data["Token"] = p.takeOnce(req.Context(), secretSetupAPIToken)
		data["GeneratedPassword"] = p.takeOnce(req.Context(), secretSetupPassword)
	case 4:
		if secret := p.pending(req.Context(), secretSetupTOTP); secret != "" {
			data["TOTPSecret"] = secret
			data["TOTPURI"] = TOTPURI(p.appName(req.Context()), p.setupUsername(req.Context(), sess), secret)
		}
	case 6:
		data["RecoveryCodes"] = p.takeOnceList(req.Context(), secretSetupRecovery)
		data["Username"] = p.setupUsername(req.Context(), sess)
	}

	ctx.Data = data
	status := http.StatusOK
	if formError != "" {
		status = http.StatusBadRequest
	}
	p.renderStatus(w, req, status, "setup", ctx)
}

// setupDefaults supplies the pre-filled values of the wizard forms.
func (p *Panel) setupDefaults(ctx context.Context) map[string]string {
	opts := p.renderer.Options()
	values := map[string]string{
		"Username": "administrator",
		"AppName":  opts.AppName,
		"FQDN":     "",
		"Mode":     "production",
		"Timezone": time.Local.String(),
	}
	stored, err := p.settingsWithPrefix(ctx, "server.")
	if err != nil {
		return values
	}
	if v := stored[settingAppName]; v != "" {
		values["AppName"] = v
	}
	if v := stored[settingFQDN]; v != "" {
		values["FQDN"] = v
	}
	if v := stored[settingMode]; v != "" {
		values["Mode"] = v
	}
	if v := stored[settingTimezone]; v != "" {
		values["Timezone"] = v
	}
	return values
}

// appName returns the configured application name for display.
func (p *Panel) appName(ctx context.Context) string {
	if value, ok, err := p.setting(ctx, settingAppName); err == nil && ok && value != "" {
		return value
	}
	if name := p.renderer.Options().AppName; name != "" {
		return name
	}
	return "cashp"
}

// setupUsername returns the username of the admin the wizard created.
func (p *Panel) setupUsername(ctx context.Context, sess *sessionRecord) string {
	if sess == nil || sess.AdminID == "" {
		return ""
	}
	rec, err := p.adminByID(ctx, sess.AdminID)
	if err != nil {
		return ""
	}
	return rec.Username
}

// pending reads a secret the wizard is holding between steps. Wizard secrets
// live in the encrypted secret store, never in the settings table.
func (p *Panel) pending(ctx context.Context, name string) string {
	value, err := p.readSecret(ctx, name)
	if err != nil {
		return ""
	}
	return value
}

// takeOnce reads a wizard secret and deletes it, so a value shown once is not
// recoverable by reloading the page.
func (p *Panel) takeOnce(ctx context.Context, name string) string {
	value := p.pending(ctx, name)
	if value != "" {
		_ = p.deleteSecret(ctx, name)
	}
	return value
}

// takeOnceList reads and clears a newline-separated wizard secret.
func (p *Panel) takeOnceList(ctx context.Context, name string) []string {
	value := p.takeOnce(ctx, name)
	if value == "" {
		return nil
	}
	return strings.Split(value, "\n")
}

// handleSetupPost drives the wizard. Every step validates its own input and
// records progress before advancing.
func (p *Panel) handleSetupPost(w http.ResponseWriter, req *http.Request) {
	if !p.requirePost(w, req) {
		return
	}
	if p.SetupComplete(req.Context()) {
		p.redirect(w, req, "", "info", "Setup has already been completed.")
		return
	}

	if req.PostFormValue("action") == "token" {
		p.handleSetupToken(w, req)
		return
	}

	sess, err := p.setupSession(req)
	if err != nil {
		p.renderSetupGate(w, req, "That setup session expired. Enter the setup token again.")
		return
	}

	step, err := strconv.Atoi(req.PostFormValue("step"))
	if err != nil || step != p.setupStep(req.Context()) {
		p.renderSetupStep(w, req, sess, p.setupStep(req.Context()), "That step is no longer current.")
		return
	}

	switch step {
	case 1:
		p.setupCreateAdmin(w, req, sess)
	case 2:
		p.advanceSetup(w, req, sess, 3)
	case 3:
		p.setupServerConfig(w, req, sess)
	case 4:
		p.setupSecurity(w, req, sess)
	case 5:
		p.setupServices(w, req, sess)
	case 6:
		p.setupComplete(w, req, sess)
	default:
		p.renderSetupStep(w, req, sess, 1, "That step is not valid.")
	}
}

// handleSetupToken validates the one-time token and opens a wizard session.
func (p *Panel) handleSetupToken(w http.ResponseWriter, req *http.Request) {
	if allowed, _ := loginLimiter.Allow(clientKey(req)); !allowed {
		p.renderer.RenderError(w, req, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Try again later.")
		return
	}

	ok, err := p.redeemSetupToken(req.Context(), req.PostFormValue("token"))
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	if !ok {
		p.recordAudit(req.Context(), "setup", "setup_token_rejected", "", "", "invalid or spent setup token")
		p.renderSetupGate(w, req, "That token was not accepted.")
		return
	}

	value, err := p.createSession(req.Context(), "", sessionKindSetup, setupSessionTTL)
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	p.setSessionCookie(w, req, value, int(setupSessionTTL.Seconds()))
	_ = p.setSetupStep(req.Context(), 1)
	p.recordAudit(req.Context(), "setup", "setup_token_redeemed", "", "", "setup wizard opened")
	http.Redirect(w, req, p.url("config/setup"), http.StatusSeeOther)
}

// setupCreateAdmin performs step 1: the primary administrator account.
func (p *Panel) setupCreateAdmin(w http.ResponseWriter, req *http.Request, sess *sessionRecord) {
	username := strings.ToLower(strings.TrimSpace(req.PostFormValue("username")))
	if username == "" {
		username = "administrator"
	}
	if err := ValidateUsername(username); err != nil {
		p.renderSetupStep(w, req, sess, 1, "Username: "+err.Error())
		return
	}

	email := strings.TrimSpace(req.PostFormValue("account_email"))
	if !validEmail(email) {
		p.renderSetupStep(w, req, sess, 1, "Enter a valid account email address.")
		return
	}

	password := req.PostFormValue("password")
	generated := ""
	if password == "" {
		value, err := generatePassword()
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		password, generated = value, value
	} else if password != req.PostFormValue("password_confirm") {
		p.renderSetupStep(w, req, sess, 1, "The two passwords did not match.")
		return
	}
	if err := validatePassword(password); err != nil {
		p.renderSetupStep(w, req, sess, 1, err.Error())
		return
	}

	hashed, err := security.HashPassword(password)
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	count, err := p.countAdmins(req.Context())
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	if count > 0 {
		p.renderSetupStep(w, req, sess, 1, "An administrator already exists.")
		return
	}

	rec, err := p.createAdmin(req.Context(), username, hashed, email, true)
	if err != nil {
		p.renderSetupStep(w, req, sess, 1, "That account could not be created. Choose a different username.")
		return
	}

	if _, err := p.db.ExecContext(req.Context(), database.TimeoutWrite,
		`UPDATE admin_sessions SET admin_id = ? WHERE token_hash = ?`,
		rec.ID, security.HashToken(sessionValue(req))); err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	token, err := p.createAPIToken(req.Context(), rec.ID, "setup")
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	if err := p.storeSecret(req.Context(), secretSetupAPIToken, token); err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	if generated != "" {
		if err := p.storeSecret(req.Context(), secretSetupPassword, generated); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
	}

	p.recordAudit(req.Context(), "setup", "primary_admin_created", username, username, "primary administrator created by setup wizard")
	p.advanceSetup(w, req, sess, 2)
}

// setupServerConfig performs step 3: name, domain, mode and timezone.
func (p *Panel) setupServerConfig(w http.ResponseWriter, req *http.Request, sess *sessionRecord) {
	mode := req.PostFormValue("mode")
	if mode != "production" && mode != "development" {
		p.renderSetupStep(w, req, sess, 3, "Choose either production or development mode.")
		return
	}
	zone := strings.TrimSpace(req.PostFormValue("timezone"))
	if zone != "" {
		if _, err := time.LoadLocation(zone); err != nil {
			p.renderSetupStep(w, req, sess, 3, "That timezone is not recognised.")
			return
		}
	}

	actor := p.setupUsername(req.Context(), sess)
	for key, value := range map[string]string{
		settingAppName:  strings.TrimSpace(req.PostFormValue("app_name")),
		settingFQDN:     strings.ToLower(strings.TrimSpace(req.PostFormValue("fqdn"))),
		settingMode:     mode,
		settingTimezone: zone,
	} {
		if err := p.putSetting(req.Context(), key, value, actor); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
	}
	p.recordAudit(req.Context(), "setup", "server_configured", actor, "", "server identity configured")
	p.advanceSetup(w, req, sess, 4)
}

// setupSecurity performs step 4: backup encryption and optional TOTP.
func (p *Panel) setupSecurity(w http.ResponseWriter, req *http.Request, sess *sessionRecord) {
	actor := p.setupUsername(req.Context(), sess)

	if passphrase := req.PostFormValue("backup_password"); passphrase != "" {
		if err := p.storeSecret(req.Context(), settingBackupKeyName, passphrase); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		p.recordAudit(req.Context(), "setup", "backup_password_set", actor, "", "backup encryption passphrase stored")
	}

	if req.PostFormValue("enable_2fa") == "" {
		p.advanceSetup(w, req, sess, 5)
		return
	}

	secret := p.pending(req.Context(), secretSetupTOTP)
	if secret == "" {
		generated, err := GenerateTOTPSecret()
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		if err := p.storeSecret(req.Context(), secretSetupTOTP, generated); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		p.renderSetupStep(w, req, sess, 4, "Scan the key below, then enter the six-digit code to confirm.")
		return
	}

	if !VerifyTOTP(secret, req.PostFormValue("totp_code"), time.Now()) {
		p.renderSetupStep(w, req, sess, 4, "That code was not accepted. Try the next one your app shows.")
		return
	}

	if err := p.enableTOTP(req.Context(), sess.AdminID, secret); err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	_ = p.deleteSecret(req.Context(), secretSetupTOTP)
	p.recordAudit(req.Context(), "setup", "admin_2fa_enabled", actor, actor, "two-factor authentication enabled")
	p.advanceSetup(w, req, sess, 5)
}

// enableTOTP stores a verified secret and issues fresh recovery codes.
func (p *Panel) enableTOTP(ctx context.Context, adminID, secret string) error {
	if err := p.storeTOTPSecret(ctx, adminID, secret, true); err != nil {
		return err
	}
	codes, err := GenerateRecoveryCodes()
	if err != nil {
		return err
	}
	if err := p.storeRecoveryCodes(ctx, adminID, codes); err != nil {
		return err
	}
	return p.storeSecret(ctx, secretSetupRecovery, strings.Join(codes, "\n"))
}

// setupServices performs step 5: certificate source and multi-user.
func (p *Panel) setupServices(w http.ResponseWriter, req *http.Request, sess *sessionRecord) {
	source := req.PostFormValue("cert_source")
	switch source {
	case "acme", "self-signed", "manual":
	default:
		p.renderSetupStep(w, req, sess, 5, "Choose a certificate source.")
		return
	}

	actor := p.setupUsername(req.Context(), sess)
	multiUser := "false"
	if req.PostFormValue("multi_user") != "" {
		multiUser = "true"
	}
	if err := p.putSetting(req.Context(), settingCertSource, source, actor); err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	if err := p.putSetting(req.Context(), settingMultiUser, multiUser, actor); err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	p.recordAudit(req.Context(), "setup", "services_configured", actor, "", "optional services configured")
	p.advanceSetup(w, req, sess, 6)
}

// setupComplete performs step 6: mark setup done, invalidate every remaining
// setup token, and sign the new administrator in.
func (p *Panel) setupComplete(w http.ResponseWriter, req *http.Request, sess *sessionRecord) {
	if sess.AdminID == "" {
		p.renderSetupStep(w, req, sess, 1, "Create the administrator account first.")
		return
	}
	rec, err := p.adminByID(req.Context(), sess.AdminID)
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	if _, err := p.db.ExecContext(req.Context(), database.TimeoutWrite,
		`UPDATE admin_setup_tokens SET used_at = ? WHERE used_at = 0`, time.Now().Unix()); err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	for _, name := range []string{secretSetupAPIToken, secretSetupPassword, secretSetupRecovery, secretSetupTOTP} {
		_ = p.deleteSecret(req.Context(), name)
	}
	if err := p.putSetting(req.Context(), settingSetupComplete, "true", rec.Username); err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	_ = p.setSetupStep(req.Context(), 6)
	_ = p.deleteSession(req.Context(), sessionValue(req))

	p.recordAudit(req.Context(), "setup", "setup_completed", rec.Username, "", "setup wizard completed")
	p.completeLogin(w, req, rec, false)
}

// advanceSetup records the next step and redirects, so a refresh never repeats
// a submission.
func (p *Panel) advanceSetup(w http.ResponseWriter, req *http.Request, sess *sessionRecord, step int) {
	if err := p.setSetupStep(req.Context(), step); err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	http.Redirect(w, req, p.url("config/setup"), http.StatusSeeOther)
}

// storeSecret encrypts and stores an operator-supplied secret.
func (p *Panel) storeSecret(ctx context.Context, name, value string) error {
	key, err := p.wrapKey(ctx)
	if err != nil {
		return err
	}
	ciphertext, err := security.Encrypt(key, []byte(value))
	if err != nil {
		return err
	}
	encoded := hex.EncodeToString(ciphertext)
	now := time.Now().Unix()

	var existing string
	row := p.db.QueryRowContext(ctx, database.TimeoutSelect, `SELECT name FROM admin_secrets WHERE name = ?`, name)
	switch err := row.Scan(&existing); {
	case err == nil:
		_, err := p.db.ExecContext(ctx, database.TimeoutWrite,
			`UPDATE admin_secrets SET secret_value = ? WHERE name = ?`, encoded, name)
		return err
	case errors.Is(err, sql.ErrNoRows):
		_, err := p.db.ExecContext(ctx, database.TimeoutWrite,
			`INSERT INTO admin_secrets (name, secret_value, created_at) VALUES (?, ?, ?)`, name, encoded, now)
		return err
	default:
		return err
	}
}

// readSecret decrypts a stored operator secret. It returns an empty string when
// nothing is stored under that name.
func (p *Panel) readSecret(ctx context.Context, name string) (string, error) {
	var encoded string
	row := p.db.QueryRowContext(ctx, database.TimeoutSelect, `SELECT secret_value FROM admin_secrets WHERE name = ?`, name)
	if err := row.Scan(&encoded); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	if encoded == "" {
		return "", nil
	}

	ciphertext, err := hex.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	key, err := p.wrapKey(ctx)
	if err != nil {
		return "", err
	}
	plaintext, err := security.Decrypt(key, ciphertext)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// deleteSecret removes a stored operator secret.
func (p *Panel) deleteSecret(ctx context.Context, name string) error {
	_, err := p.db.ExecContext(ctx, database.TimeoutWrite, `DELETE FROM admin_secrets WHERE name = ?`, name)
	return err
}

// hasSecret reports whether an operator secret is stored, without reading it.
func (p *Panel) hasSecret(ctx context.Context, name string) bool {
	var stored string
	row := p.db.QueryRowContext(ctx, database.TimeoutSelect, `SELECT name FROM admin_secrets WHERE name = ?`, name)
	return row.Scan(&stored) == nil
}

// passwordAlphabet is the character set of a generated administrator password.
const passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#$%^&*"

// generatePassword returns a 24-character random password.
func generatePassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("admin: generate password: %w", err)
	}
	var b strings.Builder
	for _, v := range buf {
		b.WriteByte(passwordAlphabet[int(v)%len(passwordAlphabet)])
	}
	return b.String(), nil
}

// validatePassword enforces the administrator password policy: at least twelve
// characters with three of the four character classes.
func validatePassword(password string) error {
	if len(password) < 12 {
		return fmt.Errorf("the password must be at least 12 characters long")
	}
	if len(password) > 256 {
		return fmt.Errorf("the password must be at most 256 characters long")
	}
	if strings.TrimSpace(password) != password {
		return fmt.Errorf("the password must not start or end with a space")
	}

	var lower, upper, digit, symbol bool
	for _, r := range password {
		switch {
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			symbol = true
		}
	}
	classes := 0
	for _, present := range []bool{lower, upper, digit, symbol} {
		if present {
			classes++
		}
	}
	if classes < 3 {
		return fmt.Errorf("the password must mix upper case, lower case, digits and symbols")
	}
	return nil
}

// validEmail performs the shape check an address must pass before it is stored.
// Deliverability is proven by the verification email, not by a pattern.
func validEmail(value string) bool {
	if len(value) < 3 || len(value) > 254 || strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	at := strings.LastIndex(value, "@")
	if at <= 0 || at == len(value)-1 {
		return false
	}
	domain := value[at+1:]
	return strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".")
}
