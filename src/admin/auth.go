package admin

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/notify"
	"github.com/webappsgo/cashp/src/security"
)

// loginRule bounds sign-in attempts per client. Admin sign-in is the most
// valuable target in the application, so it is stricter than a normal write.
var loginRule = security.Rule{Requests: 5, Window: 15 * time.Minute}

// loginLimiter enforces loginRule across every panel instance in the process.
var loginLimiter = security.NewLimiter(loginRule)

// genericAuthError is the only message a failed sign-in ever produces. It never
// reveals whether the username exists, is disabled, or has 2FA enabled.
const genericAuthError = "Those credentials were not accepted."

// handleRoot renders the panel root: the sign-in form when signed out, the
// second-factor challenge mid-login, and the dashboard once authenticated.
func (p *Panel) handleRoot(w http.ResponseWriter, req *http.Request) {
	if rec, err := p.currentAdmin(req); err == nil {
		p.renderDashboard(w, req, rec)
		return
	} else if !errors.Is(err, errNoRow) {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	if sess, err := p.currentSession(req, sessionKindPending); err == nil {
		p.renderChallenge(w, req, sess, "")
		return
	}
	p.renderLogin(w, req, "")
}

// renderLogin renders the sign-in form.
func (p *Panel) renderLogin(w http.ResponseWriter, req *http.Request, formError string) {
	ctx := p.newContext(w, req, nil, "Sign in", "Administrative sign-in.")
	ctx.PageClass = "panel panel-auth"

	count, err := p.countAdmins(req.Context())
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	ctx.Data = map[string]any{
		"Error":      formError,
		"NeedsSetup": count == 0,
		"SetupURL":   p.url("config/setup"),
		"Username":   req.PostFormValue("username"),
	}
	status := http.StatusOK
	if formError != "" {
		status = http.StatusUnauthorized
	}
	p.renderStatus(w, req, status, "login", ctx)
}

// renderChallenge renders the second-factor prompt for a half-completed login.
func (p *Panel) renderChallenge(w http.ResponseWriter, req *http.Request, sess *sessionRecord, formError string) {
	ctx := p.newContext(w, req, nil, "Two-factor verification", "Confirm the second authentication factor.")
	ctx.PageClass = "panel panel-auth"
	ctx.Data = map[string]any{
		"Error":   formError,
		"Expires": sess.ExpiresAt,
	}
	status := http.StatusOK
	if formError != "" {
		status = http.StatusUnauthorized
	}
	p.renderStatus(w, req, status, "challenge", ctx)
}

// handleRootPost processes the sign-in, second-factor and sign-out forms.
func (p *Panel) handleRootPost(w http.ResponseWriter, req *http.Request) {
	if !p.requirePost(w, req) {
		return
	}
	switch req.PostFormValue("action") {
	case "login":
		p.handleLogin(w, req)
	case "verify":
		p.handleVerify(w, req)
	case "logout":
		p.handleLogout(w, req)
	default:
		p.renderer.RenderError(w, req, http.StatusBadRequest, "invalid_request", "That action is not recognised.")
	}
}

// handleLogin validates a username and password. On success it either issues a
// full session or, when the account has TOTP enabled, a short-lived pending
// session that only the second factor can upgrade.
func (p *Panel) handleLogin(w http.ResponseWriter, req *http.Request) {
	if allowed, _ := loginLimiter.Allow(clientKey(req)); !allowed {
		p.recordAudit(req.Context(), "auth", "admin_login_throttled", "", req.PostFormValue("username"), "too many attempts")
		p.renderer.RenderError(w, req, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Try again later.")
		return
	}

	username := strings.ToLower(strings.TrimSpace(req.PostFormValue("username")))
	password := req.PostFormValue("password")

	rec, err := p.adminByUsername(req.Context(), username)
	if err != nil && !errors.Is(err, errNoRow) {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	if rec == nil || rec.Disabled {
		// A dummy verification keeps the response time of an unknown username
		// close to that of a known one.
		_, _, _ = security.VerifyPassword(dummyHash, password)
		p.recordAudit(req.Context(), "auth", "admin_login_failed", "", username, "unknown or disabled account")
		p.renderLogin(w, req, genericAuthError)
		return
	}

	ok, needsRehash, err := security.VerifyPassword(rec.PasswordHash, password)
	if err != nil || !ok {
		p.recordAudit(req.Context(), "auth", "admin_login_failed", "", username, "bad password")
		p.renderLogin(w, req, genericAuthError)
		return
	}
	if needsRehash {
		if hashed, hashErr := security.HashPassword(password); hashErr == nil {
			_ = p.updateAdminPassword(req.Context(), rec.ID, hashed)
		}
	}

	if rec.TOTPEnabled {
		value, err := p.createSession(req.Context(), rec.ID, sessionKindPending, mfaSessionTTL)
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		p.setSessionCookie(w, req, value, int(mfaSessionTTL.Seconds()))
		http.Redirect(w, req, p.base(), http.StatusSeeOther)
		return
	}

	p.completeLogin(w, req, rec, req.PostFormValue("remember") != "")
}

// handleVerify checks a TOTP code or a recovery code and upgrades a pending
// session into a full one.
func (p *Panel) handleVerify(w http.ResponseWriter, req *http.Request) {
	sess, err := p.currentSession(req, sessionKindPending)
	if err != nil {
		p.clearSessionCookie(w, req)
		p.renderLogin(w, req, "That sign-in attempt expired. Start again.")
		return
	}
	if allowed, _ := loginLimiter.Allow(clientKey(req)); !allowed {
		p.renderer.RenderError(w, req, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Try again later.")
		return
	}

	rec, err := p.adminByID(req.Context(), sess.AdminID)
	if err != nil || rec.Disabled {
		p.clearSessionCookie(w, req)
		p.renderLogin(w, req, genericAuthError)
		return
	}

	code := strings.TrimSpace(req.PostFormValue("code"))
	verified := false

	secret, err := p.loadTOTPSecret(req.Context(), rec)
	if err == nil && secret != "" {
		verified = VerifyTOTP(secret, code, time.Now())
	}
	if !verified {
		used, err := p.consumeRecoveryCode(req.Context(), rec.ID, normalizeRecoveryCode(code))
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		verified = used
		if used {
			p.recordAudit(req.Context(), "auth", "admin_recovery_code_used", rec.Username, rec.Username, "recovery code consumed")
		}
	}
	if !verified {
		p.recordAudit(req.Context(), "auth", "admin_mfa_failed", rec.Username, rec.Username, "invalid second factor")
		p.renderChallenge(w, req, sess, "That code was not accepted.")
		return
	}

	_ = p.deleteSession(req.Context(), sessionValue(req))
	p.completeLogin(w, req, rec, false)
}

// completeLogin issues a full session and records the sign-in.
func (p *Panel) completeLogin(w http.ResponseWriter, req *http.Request, rec *adminRecord, remember bool) {
	ttl := sessionTTL
	if remember {
		ttl = rememberTTL
	}
	value, err := p.createSession(req.Context(), rec.ID, sessionKindActive, ttl)
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	p.setSessionCookie(w, req, value, int(ttl.Seconds()))
	loginLimiter.Reset(clientKey(req))

	if _, err := p.db.ExecContext(req.Context(), database.TimeoutWrite, `UPDATE admins SET last_login_at = ? WHERE id = ?`,
		time.Now().Unix(), rec.ID); err != nil {
		return
	}
	p.recordAudit(req.Context(), "auth", "admin_login", rec.Username, rec.Username, "signed in")
	p.notify(req.Context(), notify.EventAdminLogin, map[string]string{"username": rec.Username})
	http.Redirect(w, req, p.base(), http.StatusSeeOther)
}

// handleLogout revokes the current session.
func (p *Panel) handleLogout(w http.ResponseWriter, req *http.Request) {
	if rec, err := p.currentAdmin(req); err == nil {
		p.recordAudit(req.Context(), "auth", "admin_logout", rec.Username, rec.Username, "signed out")
		p.notify(req.Context(), notify.EventAdminLogout, map[string]string{"username": rec.Username})
	}
	_ = p.deleteSession(req.Context(), sessionValue(req))
	p.clearSessionCookie(w, req)
	http.Redirect(w, req, p.base(), http.StatusSeeOther)
}

// clientKey identifies a client for rate limiting. Only the remote address is
// used: a forwarded header is attacker-controlled unless a trusted proxy has
// been configured, which is the server layer's decision, not the panel's.
func clientKey(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

// dummyHash is a real Argon2id hash of a value nobody knows. Verifying against
// it gives an unknown username the same cost as a known one.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=2$Y2FzaHBhZG1pbmR1bW15$Zm9yY29uc3RhbnR0aW1lY29tcGFyaXNvbnM"
