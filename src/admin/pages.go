package admin

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/notify"
	"github.com/webappsgo/cashp/src/security"
)

// auditPageSize is how many audit rows one log page shows.
const auditPageSize = 50

// auditExportLimit bounds a CSV export so a download can never exhaust memory.
const auditExportLimit = 5000

// inviteTTLs are the invite lifetimes PART 17 allows, in presentation order.
var inviteTTLs = []struct {
	Value string
	Label string
	TTL   time.Duration
}{
	{Value: "1h", Label: "1 hour", TTL: time.Hour},
	{Value: "6h", Label: "6 hours", TTL: 6 * time.Hour},
	{Value: "24h", Label: "24 hours", TTL: 24 * time.Hour},
	{Value: "48h", Label: "48 hours", TTL: 48 * time.Hour},
	{Value: "7d", Label: "7 days", TTL: 7 * 24 * time.Hour},
}

// inviteTTL resolves a submitted invite lifetime.
func inviteTTL(value string) (time.Duration, bool) {
	for _, candidate := range inviteTTLs {
		if candidate.Value == value {
			return candidate.TTL, true
		}
	}
	return 0, false
}

// renderDashboard renders the panel landing page for a signed-in admin.
func (p *Panel) renderDashboard(w http.ResponseWriter, req *http.Request, rec *adminRecord) {
	ctx := p.newContext(w, req, rec, "Dashboard", "Server status at a glance.")
	ctx.PageClass = "panel panel-dashboard"

	recent, err := p.recentAudit(req.Context(), "", "", 10, 0)
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	ctx.Data = map[string]any{
		"Stats":         p.hostStats(req.Context()),
		"Recent":        recent,
		"SetupComplete": p.SetupComplete(req.Context()),
		"SetupURL":      p.url("config/setup"),
		"Maintenance":   p.settingValue(req.Context(), field{Key: "maintenance.enabled", Default: "false"}) == "true",
		"Quick": []navItem{
			{Label: "Server settings", Href: p.url("config/settings")},
			{Label: "Logs", Href: p.url("config/logs")},
			{Label: "Administrators", Href: p.url("config/admins")},
			{Label: "Backup", Href: p.url("config/backup")},
		},
	}
	p.render(w, req, "dashboard", ctx)
}

// logFilter is the parsed query of a log view.
type logFilter struct {
	Category string
	Search   string
	Page     int
}

// parseLogFilter reads the filter from the query string. Filtering is a GET so
// a filtered view can be bookmarked and works without JavaScript.
func parseLogFilter(req *http.Request) logFilter {
	filter := logFilter{
		Category: strings.TrimSpace(req.URL.Query().Get("category")),
		Search:   strings.TrimSpace(req.URL.Query().Get("q")),
		Page:     1,
	}
	if len(filter.Search) > 128 {
		filter.Search = filter.Search[:128]
	}
	if page, err := strconv.Atoi(req.URL.Query().Get("page")); err == nil && page > 1 {
		filter.Page = page
	}
	return filter
}

// query renders the filter back into a query string.
func (f logFilter) query(page int) string {
	values := url.Values{}
	if f.Category != "" {
		values.Set("category", f.Category)
	}
	if f.Search != "" {
		values.Set("q", f.Search)
	}
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

// handleLogs renders the audit log viewer.
func (p *Panel) handleLogs(w http.ResponseWriter, req *http.Request) {
	rec := adminFromContext(req.Context())
	filter := parseLogFilter(req)

	// One extra row is fetched to find out whether a next page exists.
	rows, err := p.recentAudit(req.Context(), filter.Category, filter.Search, auditPageSize+1, (filter.Page-1)*auditPageSize)
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	hasNext := len(rows) > auditPageSize
	if hasNext {
		rows = rows[:auditPageSize]
	}

	categories, err := p.auditCategories(req.Context())
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	ctx := p.newContext(w, req, rec, "Logs", "Audit trail of administrative activity.")
	ctx.PageClass = "panel panel-logs"

	base := p.url("config/logs")
	data := map[string]any{
		"Rows":       rows,
		"Categories": categories,
		"Filter":     filter,
		"Page":       filter.Page,
		"HasPrev":    filter.Page > 1,
		"HasNext":    hasNext,
		"Action":     base,
		"ExportURL":  p.url("config/logs/export") + filter.query(1),
	}
	if filter.Page > 1 {
		data["PrevURL"] = base + filter.query(filter.Page-1)
	}
	if hasNext {
		data["NextURL"] = base + filter.query(filter.Page+1)
	}
	ctx.Data = data
	p.render(w, req, "logs", ctx)
}

// handleLogsExport streams the filtered audit trail as CSV.
func (p *Panel) handleLogsExport(w http.ResponseWriter, req *http.Request) {
	rec := adminFromContext(req.Context())
	filter := parseLogFilter(req)

	rows, err := p.recentAudit(req.Context(), filter.Category, filter.Search, auditExportLimit, 0)
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	filename := "audit-" + time.Now().UTC().Format("20060102-150405") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")

	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"occurred_at", "category", "event", "actor", "target", "detail"})
	for _, row := range rows {
		_ = writer.Write([]string{
			row.OccurredAt.UTC().Format(time.RFC3339),
			row.Category, row.Event, row.Actor, row.Target, row.Detail,
		})
	}
	writer.Flush()

	p.recordAudit(req.Context(), "audit", "audit_exported", rec.Username, "", fmt.Sprintf("%d rows exported", len(rows)))
}

// handleInfo renders the server information page.
func (p *Panel) handleInfo(w http.ResponseWriter, req *http.Request) {
	rec := adminFromContext(req.Context())
	ctx := p.newContext(w, req, rec, "Server info", "Runtime, host and database details.")
	ctx.PageClass = "panel panel-info"

	settings, err := p.settingsWithPrefix(req.Context(), "server.")
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	ctx.Data = map[string]any{
		"Stats":      p.hostStats(req.Context()),
		"Mode":       settings[settingMode],
		"FQDN":       settings[settingFQDN],
		"Timezone":   settings[settingTimezone],
		"AdminPath":  p.adminPath,
		"SetupDone":  p.SetupComplete(req.Context()),
		"PageCount":  len(p.pages),
		"PageNames":  p.PageNames(),
		"BackupHeld": p.hasSecret(req.Context(), settingBackupKeyName),
	}
	p.render(w, req, "info", ctx)
}

// handleHelp renders the panel's own documentation index.
func (p *Panel) handleHelp(w http.ResponseWriter, req *http.Request) {
	rec := adminFromContext(req.Context())
	ctx := p.newContext(w, req, rec, "Help", "How to operate this panel.")
	ctx.PageClass = "panel panel-help"

	account := p.base() + "/" + url.PathEscape(rec.Username)
	ctx.Data = map[string]any{
		"Topics": []map[string]string{
			{"Title": "Signing in", "Body": "The panel has its own session, separate from a user account. Signing out of the public site does not sign you out here, and an ordinary user account can never reach these pages."},
			{"Title": "Changing the panel address", "Body": "The panel path is a server configuration value. Change it in the configuration file and restart; the panel is never linked from a public page and never appears in a sitemap or in the API documentation."},
			{"Title": "Two-factor authentication", "Body": "Enable an authenticator app on your profile page. Ten recovery codes are issued at the same time; each one works once. Store them somewhere other than the device that holds the authenticator."},
			{"Title": "Losing access", "Body": "A recovery code signs you back in. With no recovery code left, another administrator must delete the account and send a new invitation. The primary administrator account cannot be deleted from the panel."},
			{"Title": "Invitations", "Body": "Administrators are added by invitation. An invitation is single use by default and expires on the schedule you choose when creating it."},
			{"Title": "Audit trail", "Body": "Every administrative change is recorded with the account that made it. The log page filters by category and free text, and exports the current filter as CSV."},
		},
		"Links": []navItem{
			{Label: "Your profile", Href: account + "/profile"},
			{Label: "Server information", Href: p.url("config/info")},
			{Label: "Audit log", Href: p.url("config/logs")},
			{Label: "Administrators", Href: p.url("config/admins")},
		},
	}
	p.render(w, req, "help", ctx)
}

// handleAdmins renders the administrators page. PART 17 forbids listing other
// administrators, so only the caller's own account, the total count, the
// currently online usernames and the pending invitations are shown.
func (p *Panel) handleAdmins(w http.ResponseWriter, req *http.Request) {
	p.renderAdmins(w, req, http.StatusOK, "")
}

// renderAdmins renders the administrators page with an optional inline result.
func (p *Panel) renderAdmins(w http.ResponseWriter, req *http.Request, status int, issuedInvite string) {
	rec := adminFromContext(req.Context())
	ctx := p.newContext(w, req, rec, "Administrators", "Invite and remove server administrators.")
	ctx.PageClass = "panel panel-admins"

	invites, err := p.pendingInvites(req.Context())
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	ctx.Data = map[string]any{
		"Invites":      invites,
		"InviteTTLs":   inviteTTLs,
		"InviteToken":  issuedInvite,
		"InviteURL":    p.inviteURL(issuedInvite),
		"RecoveryNote": "For security, other administrator accounts cannot be viewed. Each administrator manages their own credentials, and an account that has lost both its authenticator and its recovery codes has to be removed and invited again.",
	}
	p.renderStatus(w, req, status, "admins", ctx)
}

// inviteURL builds the redemption link for a freshly issued invitation.
func (p *Panel) inviteURL(token string) string {
	if token == "" {
		return ""
	}
	return strings.TrimSuffix(p.renderer.Options().BaseURL, "/") + "/server/auth/invite/server/" + url.PathEscape(token)
}

// handleAdminsPost creates and revokes invitations and removes administrators.
func (p *Panel) handleAdminsPost(w http.ResponseWriter, req *http.Request) {
	if !p.requirePost(w, req) {
		return
	}
	rec := adminFromContext(req.Context())

	switch req.PostFormValue("action") {
	case "invite":
		username := strings.ToLower(strings.TrimSpace(req.PostFormValue("username")))
		if err := ValidateUsername(username); err != nil {
			p.redirect(w, req, "config/admins", "error", "Username: "+err.Error())
			return
		}
		ttl, ok := inviteTTL(req.PostFormValue("expires"))
		if !ok {
			p.redirect(w, req, "config/admins", "error", "Choose one of the listed expiry times.")
			return
		}
		if existing, err := p.adminByUsername(req.Context(), username); err == nil && existing != nil {
			p.redirect(w, req, "config/admins", "error", "That username is already taken.")
			return
		} else if err != nil && !errors.Is(err, errNoRow) {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}

		token, err := p.createInvite(req.Context(), username, rec.Username, ttl, 1)
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		p.recordAudit(req.Context(), "admins", "admin_invited", rec.Username, username, "invitation created")
		p.renderAdmins(w, req, http.StatusOK, token)

	case "revoke_invite":
		id := req.PostFormValue("id")
		affected, err := p.revokeInvite(req.Context(), id)
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		if affected == 0 {
			p.redirect(w, req, "config/admins", "error", "That invitation is no longer pending.")
			return
		}
		p.recordAudit(req.Context(), "admins", "admin_invite_revoked", rec.Username, id, "invitation revoked")
		p.redirect(w, req, "config/admins", "success", "The invitation was revoked.")

	case "remove_admin":
		username := strings.ToLower(strings.TrimSpace(req.PostFormValue("username")))
		if username == "" {
			p.redirect(w, req, "config/admins", "error", "Enter the username to remove.")
			return
		}
		// The confirmation step is the username typed a second time, so the
		// destructive action can never be one careless click.
		if strings.ToLower(strings.TrimSpace(req.PostFormValue("confirm"))) != username {
			p.redirect(w, req, "config/admins", "error", "Type the username again to confirm the removal.")
			return
		}
		if username == rec.Username {
			p.redirect(w, req, "config/admins", "error", "You cannot remove your own account.")
			return
		}
		affected, err := p.deleteAdminByUsername(req.Context(), username)
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		if affected == 0 {
			// The same message is used whether the account is absent or is the
			// primary administrator, so the form cannot enumerate accounts.
			p.redirect(w, req, "config/admins", "error", "That account could not be removed.")
			return
		}
		p.recordAudit(req.Context(), "admins", "admin_removed", rec.Username, username, "administrator account deleted")
		p.redirect(w, req, "config/admins", "success", "The account was removed.")

	default:
		p.renderer.RenderError(w, req, http.StatusBadRequest, "invalid_request", "That action is not recognised.")
	}
}

// handleTokens renders the caller's own API tokens.
func (p *Panel) handleTokens(w http.ResponseWriter, req *http.Request) {
	p.renderTokens(w, req, "")
}

// renderTokens renders the token page, optionally showing a token once.
func (p *Panel) renderTokens(w http.ResponseWriter, req *http.Request, issued string) {
	rec := adminFromContext(req.Context())
	ctx := p.newContext(w, req, rec, "API tokens", "Tokens that act on your behalf.")
	ctx.PageClass = "panel panel-tokens"

	tokens, err := p.apiTokens(req.Context(), rec.ID)
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	ctx.Data = map[string]any{
		"Tokens":      tokens,
		"IssuedToken": issued,
	}
	p.render(w, req, "tokens", ctx)
}

// handleTokensPost issues and revokes the caller's own API tokens.
func (p *Panel) handleTokensPost(w http.ResponseWriter, req *http.Request) {
	if !p.requirePost(w, req) {
		return
	}
	rec := adminFromContext(req.Context())

	switch req.PostFormValue("action") {
	case "create":
		name := strings.TrimSpace(req.PostFormValue("name"))
		if name == "" || len(name) > 64 || strings.ContainsAny(name, "\r\n") {
			p.redirect(w, req, "config/security/tokens", "error", "Give the token a short name.")
			return
		}
		token, err := p.createAPIToken(req.Context(), rec.ID, name)
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		p.recordAudit(req.Context(), "tokens", "api_token_created", rec.Username, name, "API token issued")
		p.renderTokens(w, req, token)

	case "revoke":
		if req.PostFormValue("confirm") != "yes" {
			p.redirect(w, req, "config/security/tokens", "error", "That action needs to be confirmed.")
			return
		}
		affected, err := p.revokeAPIToken(req.Context(), rec.ID, req.PostFormValue("id"))
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		if affected == 0 {
			p.redirect(w, req, "config/security/tokens", "error", "That token is already revoked.")
			return
		}
		p.recordAudit(req.Context(), "tokens", "api_token_revoked", rec.Username, req.PostFormValue("id"), "API token revoked")
		p.redirect(w, req, "config/security/tokens", "success", "The token was revoked.")

	default:
		p.renderer.RenderError(w, req, http.StatusBadRequest, "invalid_request", "That action is not recognised.")
	}
}

// handleNodes renders the cluster node list.
func (p *Panel) handleNodes(w http.ResponseWriter, req *http.Request) {
	p.renderNodes(w, req, "")
}

// renderNodes renders the node table, optionally showing a join token once.
func (p *Panel) renderNodes(w http.ResponseWriter, req *http.Request, joinToken string) {
	rec := adminFromContext(req.Context())
	ctx := p.newContext(w, req, rec, "Cluster nodes", "Managed nodes enrolled into this server.")
	ctx.PageClass = "panel panel-nodes"

	nodes, err := p.clusterNodes(req.Context())
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	ctx.Data = map[string]any{
		"Nodes":       nodes,
		"JoinToken":   joinToken,
		"DefaultPort": p.settingValue(req.Context(), field{Key: "cluster.default_port", Default: "64581"}),
		"SettingsURL": p.url("config/cluster/add"),
	}
	p.render(w, req, "nodes", ctx)
}

// handleNodesPost enrols and removes cluster nodes.
func (p *Panel) handleNodesPost(w http.ResponseWriter, req *http.Request) {
	if !p.requirePost(w, req) {
		return
	}
	rec := adminFromContext(req.Context())

	switch req.PostFormValue("action") {
	case "enroll":
		name := strings.ToLower(strings.TrimSpace(req.PostFormValue("name")))
		if err := ValidateUsername(name); err != nil {
			p.redirect(w, req, "config/cluster/nodes", "error", "Node name: "+err.Error())
			return
		}
		address := strings.TrimSpace(req.PostFormValue("address"))
		if address == "" || len(address) > 253 || strings.ContainsAny(address, " \r\n") {
			p.redirect(w, req, "config/cluster/nodes", "error", "Enter the node's host name or address.")
			return
		}
		port, err := strconv.Atoi(strings.TrimSpace(req.PostFormValue("port")))
		if err != nil || port < 1 || port > 65535 {
			p.redirect(w, req, "config/cluster/nodes", "error", "Enter a port between 1 and 65535.")
			return
		}
		labels, err := normalizeTags(req.PostFormValue("labels"))
		if err != nil {
			p.redirect(w, req, "config/cluster/nodes", "error", "Labels: "+err.Error())
			return
		}

		token, err := p.createClusterNode(req.Context(), name, address, port, labels, rec.Username)
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		p.recordAudit(req.Context(), "cluster", "node_enrolled", rec.Username, name, "node enrolled and join token issued")
		p.renderNodes(w, req, token)

	case "remove":
		if req.PostFormValue("confirm") != "yes" {
			p.redirect(w, req, "config/cluster/nodes", "error", "That action needs to be confirmed.")
			return
		}
		id := req.PostFormValue("id")
		affected, err := p.deleteClusterNode(req.Context(), id)
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		if affected == 0 {
			p.redirect(w, req, "config/cluster/nodes", "error", "That node is no longer enrolled.")
			return
		}
		p.recordAudit(req.Context(), "cluster", "node_removed", rec.Username, id, "node removed from the cluster")
		p.redirect(w, req, "config/cluster/nodes", "success", "The node was removed.")

	default:
		p.renderer.RenderError(w, req, http.StatusBadRequest, "invalid_request", "That action is not recognised.")
	}
}

// accountLeaf returns the last path segment of an account route.
func accountLeaf(req *http.Request) string {
	parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// ownsAccount reports whether the signed-in admin owns the requested account
// route. An admin may never open another admin's pages.
func ownsAccount(req *http.Request, rec *adminRecord) bool {
	return rec != nil && req.PathValue("admin") == rec.Username
}

// handleAccountIndex redirects an admin's own root to their profile.
func (p *Panel) handleAccountIndex(w http.ResponseWriter, req *http.Request) {
	rec := adminFromContext(req.Context())
	if !ownsAccount(req, rec) {
		p.handleNotFound(w, req)
		return
	}
	http.Redirect(w, req, p.base()+"/"+url.PathEscape(rec.Username)+"/profile", http.StatusSeeOther)
}

// preferenceFields are the appearance settings an admin controls.
var preferenceFields = []field{
	{Key: "theme", Label: "Theme", Kind: kindSelect, Default: "dark", Options: []option{
		{Value: "dark", Label: "Dark"},
		{Value: "light", Label: "Light"},
		{Value: "auto", Label: "Follow the device"},
	}},
	{Key: "font_size", Label: "Font size", Kind: kindSelect, Default: "medium", Options: []option{
		{Value: "small", Label: "Small"},
		{Value: "medium", Label: "Medium"},
		{Value: "large", Label: "Large"},
	}},
	{Key: "reduce_motion", Label: "Reduce motion", Kind: kindToggle, Default: "false",
		Help: "Minimises animation and transition effects."},
	{Key: "date_format", Label: "Date format", Kind: kindSelect, Default: "2006-01-02", Options: []option{
		{Value: "2006-01-02", Label: "2006-01-02"},
		{Value: "02/01/2006", Label: "02/01/2006"},
		{Value: "01/02/2006", Label: "01/02/2006"},
	}},
	{Key: "time_format", Label: "Time format", Kind: kindSelect, Default: "24h", Options: []option{
		{Value: "24h", Label: "24 hour"},
		{Value: "12h", Label: "12 hour"},
	}},
}

// notificationCategoryLabels maps a notify.Category constant to the heading
// AI.md PART 18's Admin Notification Preferences table uses for it.
var notificationCategoryLabels = map[string]string{
	notify.CategorySecurity:  "Security",
	notify.CategoryServer:    "Server",
	notify.CategoryBackup:    "Backup",
	notify.CategoryScheduler: "Scheduler",
	notify.CategoryAdmins:    "Other Admins",
}

// notificationCategoryOrder is the fixed AI.md PART 18 display order for the
// admin notification-preference categories (Security, Server, Backup,
// Scheduler, Other Admins). Any category outside this list (e.g. the
// end-user-only categories) is never shown on the admin preferences page.
var notificationCategoryOrder = []string{
	notify.CategorySecurity,
	notify.CategoryServer,
	notify.CategoryBackup,
	notify.CategoryScheduler,
	notify.CategoryAdmins,
}

// notificationCategory groups the preferences rendered under one heading.
type notificationCategory struct {
	Label string
	Prefs []notify.Preference
}

// groupNotificationPreferences arranges an admin's notification preferences
// into the fixed AI.md PART 18 category order, dropping any category the
// admin audience never uses.
func groupNotificationPreferences(prefs []notify.Preference) []notificationCategory {
	byCategory := make(map[string][]notify.Preference, len(notificationCategoryOrder))
	for _, pref := range prefs {
		byCategory[pref.Category] = append(byCategory[pref.Category], pref)
	}
	out := make([]notificationCategory, 0, len(notificationCategoryOrder))
	for _, cat := range notificationCategoryOrder {
		items, ok := byCategory[cat]
		if !ok {
			continue
		}
		label := notificationCategoryLabels[cat]
		if label == "" {
			label = cat
		}
		out = append(out, notificationCategory{Label: label, Prefs: items})
	}
	return out
}

// pendingTOTPName is the secret-store name of a not-yet-confirmed TOTP secret.
func pendingTOTPName(adminID string) string {
	return "totp_pending_" + adminID
}

// handleAccountGet renders one of the caller's own account pages.
func (p *Panel) handleAccountGet(w http.ResponseWriter, req *http.Request) {
	rec := adminFromContext(req.Context())
	if !ownsAccount(req, rec) {
		p.handleNotFound(w, req)
		return
	}
	switch accountLeaf(req) {
	case "profile":
		p.renderProfile(w, req, rec, "")
	case "preferences":
		p.renderPreferences(w, req, rec)
	case "notifications":
		p.renderNotifications(w, req, rec)
	default:
		p.handleNotFound(w, req)
	}
}

// renderProfile renders the account page, including second-factor state.
func (p *Panel) renderProfile(w http.ResponseWriter, req *http.Request, rec *adminRecord, newCodes string) {
	ctx := p.newContext(w, req, rec, "Profile", "Your account, credentials and second factor.")
	ctx.PageClass = "panel panel-profile"

	remaining, err := p.countRecoveryCodes(req.Context(), rec.ID)
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	data := map[string]any{
		"Remaining":     remaining,
		"RecoveryCodes": splitLines(newCodes),
		"Source":        rec.Source,
		"External":      rec.Source != "" && rec.Source != "local",
	}

	// A pending secret means the admin started enabling an authenticator and
	// has not yet confirmed a code.
	if !rec.TOTPEnabled {
		if secret := p.pending(req.Context(), pendingTOTPName(rec.ID)); secret != "" {
			data["PendingSecret"] = secret
			data["PendingURI"] = TOTPURI(p.appName(req.Context()), rec.Username, secret)
		}
	}
	ctx.Data = data
	p.render(w, req, "profile", ctx)
}

// splitLines turns a newline-separated block into a slice, dropping blanks.
func splitLines(value string) []string {
	if value == "" {
		return nil
	}
	out := make([]string, 0, 10)
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// renderPreferences renders the appearance settings.
func (p *Panel) renderPreferences(w http.ResponseWriter, req *http.Request, rec *adminRecord) {
	ctx := p.newContext(w, req, rec, "Preferences", "How this panel looks for you.")
	ctx.PageClass = "panel panel-settings"

	stored, err := p.preferences(req.Context(), rec.ID)
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	ctx.Data = map[string]any{"Fields": renderPreferenceFields(preferenceFields, stored)}
	p.render(w, req, "preferences", ctx)
}

// renderNotifications renders the AI.md PART 18 admin notification
// preference categories (Security/Server/Backup/Scheduler/Other Admins),
// scoped to this admin, sourced from notify.Store rather than the generic
// admin_preferences key/value table. A nil notifier (no notify package
// wired into this Panel) degrades to an empty, informative page rather
// than failing the request.
func (p *Panel) renderNotifications(w http.ResponseWriter, req *http.Request, rec *adminRecord) {
	ctx := p.newContext(w, req, rec, "Notifications", "Which events reach you, and where.")
	ctx.PageClass = "panel panel-settings"

	var prefs []notify.Preference
	if p.notifier != nil {
		var err error
		prefs, err = p.notifier.Store().Preferences(req.Context(), notify.AudienceAdmin, rec.ID)
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
	}
	ctx.Data = map[string]any{
		"Categories":        groupNotificationPreferences(prefs),
		"NotifierAvailable": p.notifier != nil,
		"AccountEmail":      rec.AccountEmail,
		"NotificationEmail": rec.NotificationEmail,
	}
	p.render(w, req, "notifications", ctx)
}

// renderPreferenceFields fills stored values into a field list.
func renderPreferenceFields(fields []field, stored map[string]string) []renderedField {
	out := make([]renderedField, 0, len(fields))
	for _, f := range fields {
		item := renderedField{field: f, Value: f.Default}
		if value, ok := stored[f.Key]; ok && value != "" {
			item.Value = value
		}
		out = append(out, item)
	}
	return out
}

// handleAccountPost processes every account form.
func (p *Panel) handleAccountPost(w http.ResponseWriter, req *http.Request) {
	if !p.requirePost(w, req) {
		return
	}
	rec := adminFromContext(req.Context())
	if !ownsAccount(req, rec) {
		p.handleNotFound(w, req)
		return
	}
	account := url.PathEscape(rec.Username)

	switch accountLeaf(req) {
	case "profile":
		p.handleProfilePost(w, req, rec, account)
	case "preferences":
		p.savePreferences(w, req, rec, preferenceFields, account+"/preferences", "preferences_updated")
	case "notifications":
		p.handleNotificationsPost(w, req, rec, account)
	default:
		p.handleNotFound(w, req)
	}
}

// handleProfilePost handles credential and second-factor changes.
func (p *Panel) handleProfilePost(w http.ResponseWriter, req *http.Request, rec *adminRecord, account string) {
	target := account + "/profile"

	switch req.PostFormValue("action") {
	case "emails":
		accountEmail := strings.TrimSpace(req.PostFormValue("account_email"))
		notifyEmail := strings.TrimSpace(req.PostFormValue("notification_email"))
		if !validEmail(accountEmail) {
			p.redirect(w, req, target, "error", "Enter a valid account email address.")
			return
		}
		if notifyEmail != "" && !validEmail(notifyEmail) {
			p.redirect(w, req, target, "error", "Enter a valid notification email address.")
			return
		}
		if err := p.updateAdminEmails(req.Context(), rec.ID, accountEmail, notifyEmail); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		p.recordAudit(req.Context(), "account", "admin_emails_updated", rec.Username, rec.Username, "contact addresses changed")
		p.redirect(w, req, target, "success", "Your addresses were saved.")

	case "password":
		if !p.confirmPassword(w, req, rec, target) {
			return
		}
		password := req.PostFormValue("new_password")
		if password != req.PostFormValue("confirm_password") {
			p.redirect(w, req, target, "error", "The two passwords did not match.")
			return
		}
		if err := validatePassword(password); err != nil {
			p.redirect(w, req, target, "error", err.Error())
			return
		}
		hashed, err := security.HashPassword(password)
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		if err := p.updateAdminPassword(req.Context(), rec.ID, hashed); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		p.recordAudit(req.Context(), "account", "admin_password_changed", rec.Username, rec.Username, "password changed")
		p.redirect(w, req, target, "success", "Your password was changed.")

	case "totp_start":
		if rec.TOTPEnabled {
			p.redirect(w, req, target, "info", "An authenticator is already enrolled.")
			return
		}
		secret, err := GenerateTOTPSecret()
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		if err := p.storeSecret(req.Context(), pendingTOTPName(rec.ID), secret); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		p.redirect(w, req, target, "info", "Scan the key, then enter the six-digit code to finish.")

	case "totp_confirm":
		secret := p.pending(req.Context(), pendingTOTPName(rec.ID))
		if secret == "" {
			p.redirect(w, req, target, "error", "Start the enrolment again.")
			return
		}
		if !VerifyTOTP(secret, req.PostFormValue("code"), time.Now()) {
			p.redirect(w, req, target, "error", "That code was not accepted.")
			return
		}
		codes, err := p.activateTOTP(req.Context(), rec, secret)
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		p.recordAudit(req.Context(), "account", "admin_2fa_enabled", rec.Username, rec.Username, "authenticator enrolled")
		rec.TOTPEnabled = true
		p.renderProfile(w, req, rec, strings.Join(codes, "\n"))

	case "totp_disable":
		if req.PostFormValue("confirm") != "yes" {
			p.redirect(w, req, target, "error", "That action needs to be confirmed.")
			return
		}
		if !p.confirmPassword(w, req, rec, target) {
			return
		}
		if err := p.storeTOTPSecret(req.Context(), rec.ID, "", false); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		if err := p.storeRecoveryCodes(req.Context(), rec.ID, nil); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		_ = p.deleteSecret(req.Context(), pendingTOTPName(rec.ID))
		p.recordAudit(req.Context(), "account", "admin_2fa_disabled", rec.Username, rec.Username, "authenticator removed")
		p.redirect(w, req, target, "success", "Two-factor authentication was switched off.")

	case "recovery_codes":
		if !rec.TOTPEnabled {
			p.redirect(w, req, target, "error", "Enrol an authenticator first.")
			return
		}
		if !p.confirmPassword(w, req, rec, target) {
			return
		}
		codes, err := GenerateRecoveryCodes()
		if err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		if err := p.storeRecoveryCodes(req.Context(), rec.ID, codes); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		p.recordAudit(req.Context(), "account", "admin_recovery_codes_reissued", rec.Username, rec.Username, "recovery codes replaced")
		p.renderProfile(w, req, rec, strings.Join(codes, "\n"))

	case "revoke_sessions":
		if req.PostFormValue("confirm") != "yes" {
			p.redirect(w, req, target, "error", "That action needs to be confirmed.")
			return
		}
		if err := p.deleteAdminSessions(req.Context(), rec.ID); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		p.recordAudit(req.Context(), "account", "admin_sessions_revoked", rec.Username, rec.Username, "all own sessions revoked")
		p.clearSessionCookie(w, req)
		p.redirect(w, req, "", "info", "Every session was signed out.")

	default:
		p.renderer.RenderError(w, req, http.StatusBadRequest, "invalid_request", "That action is not recognised.")
	}
}

// activateTOTP confirms a pending secret and issues fresh recovery codes.
func (p *Panel) activateTOTP(ctx context.Context, rec *adminRecord, secret string) ([]string, error) {
	if err := p.storeTOTPSecret(ctx, rec.ID, secret, true); err != nil {
		return nil, err
	}
	codes, err := GenerateRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := p.storeRecoveryCodes(ctx, rec.ID, codes); err != nil {
		return nil, err
	}
	if err := p.deleteSecret(ctx, pendingTOTPName(rec.ID)); err != nil {
		return nil, err
	}
	return codes, nil
}

// confirmPassword re-checks the caller's password before a sensitive change.
func (p *Panel) confirmPassword(w http.ResponseWriter, req *http.Request, rec *adminRecord, target string) bool {
	ok, _, err := security.VerifyPassword(rec.PasswordHash, req.PostFormValue("current_password"))
	if err != nil || !ok {
		p.recordAudit(req.Context(), "account", "admin_reauth_failed", rec.Username, rec.Username, "password confirmation failed")
		p.redirect(w, req, target, "error", "Your current password was not accepted.")
		return false
	}
	return true
}

// handleNotificationsPost saves the notification categories and the addresses
// they are delivered to.
// handleNotificationsPost saves the notification email plus every toggle on
// the AI.md PART 18 admin notification preference categories. Preferences
// are stored via notify.Store, not the generic admin_preferences table; a
// nil notifier still saves the email address but has no toggles to store.
func (p *Panel) handleNotificationsPost(w http.ResponseWriter, req *http.Request, rec *adminRecord, account string) {
	target := account + "/notifications"

	notifyEmail := strings.TrimSpace(req.PostFormValue("notification_email"))
	if notifyEmail != "" && !validEmail(notifyEmail) {
		p.redirect(w, req, target, "error", "Enter a valid notification email address.")
		return
	}
	if notifyEmail != rec.NotificationEmail {
		if err := p.updateAdminEmails(req.Context(), rec.ID, rec.AccountEmail, notifyEmail); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
	}
	if p.notifier == nil {
		p.redirect(w, req, target, "success", "Your address was saved.")
		return
	}

	prefs, err := p.notifier.Store().Preferences(req.Context(), notify.AudienceAdmin, rec.ID)
	if err != nil {
		p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	toggles := make([]notify.Toggle, 0, len(prefs))
	for _, pref := range prefs {
		toggles = append(toggles, notify.Toggle{
			Event: pref.Event,
			WebUI: pref.Required || req.PostForm.Has("webui_"+pref.Event),
			Email: pref.Emailable && (pref.Required || req.PostForm.Has("email_"+pref.Event)),
		})
	}
	if err := p.notifier.Store().SavePreferences(req.Context(), notify.AudienceAdmin, rec.ID, toggles); err != nil {
		p.redirect(w, req, target, "error", "Your notification preferences could not be saved.")
		return
	}
	p.recordAudit(req.Context(), "account", "notification_prefs_updated", rec.Username, rec.Username, "notification preferences saved")
	p.redirect(w, req, target, "success", "Your preferences were saved.")
}

// savePreferences validates and stores a per-admin preference form.
func (p *Panel) savePreferences(w http.ResponseWriter, req *http.Request, rec *adminRecord, fields []field, target, event string) {
	for _, f := range fields {
		// A locked category is never read from the form: it cannot be changed.
		if f.Kind == kindReadonly {
			continue
		}
		value, err := normalizeFieldValue(f, req.PostForm.Has(f.Key), req.PostFormValue(f.Key))
		if err != nil {
			p.redirect(w, req, target, "error", f.Label+": "+err.Error())
			return
		}
		if err := p.putPreference(req.Context(), rec.ID, f.Key, value); err != nil {
			p.renderer.RenderError(w, req, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
	}
	p.recordAudit(req.Context(), "account", event, rec.Username, rec.Username, "preferences saved")
	p.redirect(w, req, target, "success", "Your preferences were saved.")
}

// RedeemInvite consumes an administrator invitation and creates the account it
// was issued for. It is called by src/auth, which owns the redemption route at
// /server/auth/invite/server/{token}.
func (p *Panel) RedeemInvite(ctx context.Context, token, username, password, accountEmail string) error {
	username = strings.ToLower(strings.TrimSpace(username))
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if !validEmail(accountEmail) {
		return fmt.Errorf("admin: a valid account email address is required")
	}
	if err := validatePassword(password); err != nil {
		return err
	}

	var (
		id       string
		reserved string
		maxUses  int
	)
	row := p.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT id, username, max_uses FROM admin_invites
		 WHERE token_hash = ? AND revoked_at = 0 AND expires_at > ? AND uses < max_uses`,
		security.HashToken(strings.TrimSpace(token)), time.Now().Unix())
	if err := row.Scan(&id, &reserved, &maxUses); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("admin: that invitation is not valid")
		}
		return err
	}
	if reserved != "" && reserved != username {
		return fmt.Errorf("admin: that invitation was issued for a different username")
	}

	// The UPDATE is the check: two concurrent redemptions cannot both consume
	// the last use of an invitation.
	res, err := p.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE admin_invites SET uses = uses + 1 WHERE id = ? AND uses < max_uses AND revoked_at = 0`, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("admin: that invitation is not valid")
	}

	hashed, err := security.HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := p.createAdmin(ctx, username, hashed, accountEmail, false); err != nil {
		return fmt.Errorf("admin: that account could not be created")
	}
	p.recordAudit(ctx, "admins", "admin_invite_redeemed", username, username, "administrator account created from invitation")
	return nil
}
