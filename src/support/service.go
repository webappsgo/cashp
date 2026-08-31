package support

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/logging"
	"github.com/webappsgo/cashp/src/security"
)

// Notifier is the narrow slice of the notification system the support package
// uses. src/server supplies an implementation when notifications are
// configured; every call site guards against a nil Notifier so support keeps
// working on an installation with notifications switched off.
type Notifier interface {
	// Notify delivers one support event. Fields carry the event payload and
	// must never contain credentials, tokens, or filesystem paths.
	Notify(ctx context.Context, event string, userID int64, fields map[string]string) error
}

// Options configures a Service.
type Options struct {
	// DB is the shared database handle.
	DB *database.DB
	// Limits is the shared rate limit registry.
	Limits *security.Limits
	// CSRFSecret keys the CSRF tokens support forms carry.
	CSRFSecret []byte
	// Identity resolves the caller. src/server supplies it; when nil, every
	// request is treated as an unauthenticated guest.
	Identity func(*http.Request) Identity
	// Notifier delivers support notifications; may be nil.
	Notifier Notifier
	// Funcs supplies the shared template function map so support pages format
	// dates and numbers exactly like the rest of the panel; may be nil.
	Funcs template.FuncMap
	// AttachmentDir is the directory ticket attachments are written under.
	AttachmentDir string
	// APIVersion is the API version segment routes are mounted under. It is
	// supplied by the server so the version is never hardcoded here.
	APIVersion string
	// BasePath is the web UI mount point, defaulting to "/support".
	BasePath string
	// SiteName is used in notification subjects.
	SiteName string
	// Logger receives support diagnostics; nil uses the shared logger.
	Logger *slog.Logger
	// Now supplies the current time; nil uses time.Now. Tests replace it.
	Now func() time.Time
}

// Service is the support subsystem. One instance serves the whole installation.
type Service struct {
	store    *Store
	opts     Options
	limits   *security.Limits
	notifier Notifier

	// mode holds the in-memory support-mode sessions. Support mode is session
	// state and is deliberately never persisted: it disappears on logout, on
	// expiry, and on restart.
	modeMu sync.RWMutex
	mode   map[string]SupportMode

	// kbIndex is the published knowledge base reduced to fixed keyword sets. It
	// is rebuilt when an article's state changes, so answering a question costs
	// no database work and involves no inference of any kind.
	kbMu    sync.RWMutex
	kbIndex []kbEntry
}

// SupportMode is one agent's active support-mode session. It is explicit,
// time-boxed, and recorded in the audit log at both ends.
type SupportMode struct {
	// SessionID is the browser session the mode belongs to.
	SessionID string
	// AgentUserID is the real human acting. Every action taken under support
	// mode is audited against this id, never against the tenant being helped.
	AgentUserID int64
	// DisplayName is the byline users see. It is the agent's configured
	// display name and never a role label.
	DisplayName string
	// Reason is the operator-supplied justification.
	Reason string
	// EnteredAt is when the session started.
	EnteredAt int64
	// ExpiresAt is when it lapses on its own.
	ExpiresAt int64
}

// SupportModeTTL is how long a support-mode session lasts before it lapses.
const SupportModeTTL = 4 * time.Hour

// New builds a Service.
func New(opts Options) (*Service, error) {
	if opts.DB == nil {
		return nil, errors.New(errors.CodeInternal, 500, "Support is not configured")
	}
	if opts.BasePath == "" {
		opts.BasePath = "/support"
	}
	if opts.APIVersion == "" {
		opts.APIVersion = "v1"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	limits := opts.Limits
	if limits == nil {
		limits = security.NewLimits()
	}
	limits.Set(limitTicketCreate, security.Rule{Requests: 10, Window: time.Hour})
	limits.Set(limitChatStart, security.Rule{Requests: 10, Window: time.Hour})
	limits.Set(limitBotMessage, security.Rule{Requests: 60, Window: time.Hour})

	return &Service{
		store:    NewStore(opts.DB),
		opts:     opts,
		limits:   limits,
		notifier: opts.Notifier,
		mode:     map[string]SupportMode{},
	}, nil
}

// Rate limit names registered by the support package.
const (
	limitTicketCreate = "support_ticket_create"
	limitChatStart    = "support_chat_start"
	limitBotMessage   = "support_bot_message"
)

// Store exposes the data layer so the scheduler tasks and tests can reach it
// without a second database handle.
func (s *Service) Store() *Store { return s.store }

// nowUnix returns the service clock in Unix seconds.
func (s *Service) nowUnix() int64 { return s.opts.Now().UTC().Unix() }

// logger returns the service logger.
func (s *Service) logger() *slog.Logger {
	if s.opts.Logger != nil {
		return s.opts.Logger
	}
	return logging.L()
}

// Bootstrap seeds the configuration a fresh installation needs. It is
// idempotent and safe to call on every start.
func (s *Service) Bootstrap(ctx context.Context) error {
	if err := s.seedSLAPolicies(ctx); err != nil {
		return err
	}
	if err := s.seedSettings(ctx); err != nil {
		return err
	}
	return s.RebuildKBIndex(ctx)
}

// Configuration keys. Every support setting lives in the database and is edited
// through the admin UI; the package reads no environment variable.
const (
	SettingChatEnabled       = "chat.enabled"
	SettingChatMaxConcurrent = "chat.max_concurrent"
	SettingChatOpenMinute    = "chat.business_open_minute"
	SettingChatCloseMinute   = "chat.business_close_minute"
	SettingChatDays          = "chat.business_days"
	SettingAutoCloseHours    = "tickets.auto_close_hours"
	SettingAttachmentMaxKB   = "tickets.attachment_max_kb"
	SettingKBPublicEnabled   = "kb.public_enabled"
)

// defaultSettings are the values a fresh installation starts with.
var defaultSettings = map[string]string{
	SettingChatEnabled:       "0",
	SettingChatMaxConcurrent: "20",
	SettingChatOpenMinute:    "540",
	SettingChatCloseMinute:   "1020",
	SettingChatDays:          "1,2,3,4,5",
	SettingAutoCloseHours:    "72",
	SettingAttachmentMaxKB:   "10240",
	SettingKBPublicEnabled:   "1",
}

// seedSettings writes any missing configuration row with its default.
func (s *Service) seedSettings(ctx context.Context) error {
	for key, value := range defaultSettings {
		_, ok, err := s.store.Setting(ctx, key)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		if err := s.store.SetSetting(ctx, key, value, s.nowUnix()); err != nil {
			return err
		}
	}
	return nil
}

// settingInt reads an integer setting, falling back to its default.
func (s *Service) settingInt(ctx context.Context, key string) int {
	fallback, _ := strconv.Atoi(defaultSettings[key])
	raw, ok, err := s.store.Setting(ctx, key)
	if err != nil || !ok {
		return fallback
	}
	value, convErr := strconv.Atoi(strings.TrimSpace(raw))
	if convErr != nil {
		return fallback
	}
	return value
}

// settingBool reads a boolean setting stored as "0" or "1".
func (s *Service) settingBool(ctx context.Context, key string) bool {
	return s.settingInt(ctx, key) != 0
}

// settingString reads a string setting, falling back to its default.
func (s *Service) settingString(ctx context.Context, key string) string {
	raw, ok, err := s.store.Setting(ctx, key)
	if err != nil || !ok {
		return defaultSettings[key]
	}
	return raw
}

// RoleOf derives the caller's support role. The support role is never stored on
// the user record and never returned in a user-facing response.
func (s *Service) RoleOf(ctx context.Context, id Identity) string {
	if !id.Authenticated || id.UserID == 0 {
		return RoleGuest
	}
	if id.GlobalAdmin {
		return RoleAdmin
	}
	agent, err := s.store.AgentByUser(ctx, id.UserID)
	if err == nil && agent.Enabled {
		return RoleAgent
	}
	return RoleUser
}

// IsStaff reports whether the caller may use the agent workspace at all.
func (s *Service) IsStaff(ctx context.Context, id Identity) bool {
	role := s.RoleOf(ctx, id)
	return role == RoleAgent || role == RoleAdmin
}

// EnterSupportMode starts a time-boxed support-mode session and audits it.
func (s *Service) EnterSupportMode(ctx context.Context, id Identity, reason string) (SupportMode, error) {
	if !s.IsStaff(ctx, id) {
		return SupportMode{}, errors.New(errors.CodeForbidden, 403, "Support mode is limited to support staff")
	}
	if id.SessionID == "" {
		return SupportMode{}, errors.New(errors.CodeBadRequest, 400, "A session is required to enter support mode")
	}
	reason = truncate(clean(reason), 200)
	if reason == "" {
		return SupportMode{}, errors.New(errors.CodeValidation, 400, "A reason is required to enter support mode")
	}

	display := s.AgentDisplayName(ctx, id)
	at := s.nowUnix()
	mode := SupportMode{
		SessionID:   id.SessionID,
		AgentUserID: id.UserID,
		DisplayName: display,
		Reason:      reason,
		EnteredAt:   at,
		ExpiresAt:   at + int64(SupportModeTTL/time.Second),
	}

	s.modeMu.Lock()
	s.mode[id.SessionID] = mode
	s.modeMu.Unlock()

	if err := s.store.TouchAgent(ctx, id.UserID, at); err != nil {
		return SupportMode{}, err
	}
	if err := s.audit(ctx, id, AuditEntry{
		Action:     "support_mode.enter",
		EntityType: "agent",
		EntityID:   strconv.FormatInt(id.UserID, 10),
		Detail:     reason,
	}); err != nil {
		return SupportMode{}, err
	}
	logging.Audit().Info("support mode entered",
		slog.Int64("actor_id", id.UserID),
		slog.String("reason", reason))
	return mode, nil
}

// ExitSupportMode ends a support-mode session and audits it.
func (s *Service) ExitSupportMode(ctx context.Context, id Identity) error {
	s.modeMu.Lock()
	mode, active := s.mode[id.SessionID]
	delete(s.mode, id.SessionID)
	s.modeMu.Unlock()

	if !active {
		return nil
	}
	if err := s.audit(ctx, id, AuditEntry{
		Action:     "support_mode.exit",
		EntityType: "agent",
		EntityID:   strconv.FormatInt(mode.AgentUserID, 10),
		Detail:     mode.Reason,
	}); err != nil {
		return err
	}
	logging.Audit().Info("support mode exited", slog.Int64("actor_id", mode.AgentUserID))
	return nil
}

// SupportModeFor returns the caller's active support-mode session, if any. An
// expired session is dropped rather than honoured.
func (s *Service) SupportModeFor(id Identity) (SupportMode, bool) {
	if id.SessionID == "" {
		return SupportMode{}, false
	}
	s.modeMu.RLock()
	mode, ok := s.mode[id.SessionID]
	s.modeMu.RUnlock()
	if !ok {
		return SupportMode{}, false
	}
	if mode.AgentUserID != id.UserID || mode.ExpiresAt <= s.nowUnix() {
		s.modeMu.Lock()
		delete(s.mode, id.SessionID)
		s.modeMu.Unlock()
		return SupportMode{}, false
	}
	return mode, true
}

// requireSupportMode is the guard every agent and admin action passes through.
func (s *Service) requireSupportMode(ctx context.Context, id Identity) (SupportMode, error) {
	if !s.IsStaff(ctx, id) {
		return SupportMode{}, errors.New(errors.CodeForbidden, 403, "Permission denied")
	}
	mode, ok := s.SupportModeFor(id)
	if !ok {
		return SupportMode{}, errors.New(errors.CodeForbidden, 403, "Enter support mode to use the agent workspace")
	}
	if err := s.store.TouchAgent(ctx, id.UserID, s.nowUnix()); err != nil {
		return SupportMode{}, err
	}
	return mode, nil
}

// AgentDisplayName returns the byline a user sees for a member of staff. It is
// always the configured display name: no role, title, or hierarchy label is
// ever exposed to a tenant, and administrators appear exactly like agents.
func (s *Service) AgentDisplayName(ctx context.Context, id Identity) string {
	agent, err := s.store.AgentByUser(ctx, id.UserID)
	if err == nil && strings.TrimSpace(agent.DisplayName) != "" {
		return agent.DisplayName
	}
	if strings.TrimSpace(id.DisplayName) != "" {
		return id.DisplayName
	}
	return "Support"
}

// audit appends one append-only audit record, recording the real actor and
// whether the action was taken under support mode.
func (s *Service) audit(ctx context.Context, id Identity, e AuditEntry) error {
	mode, inMode := s.SupportModeFor(id)
	e.ID = newID("aud")
	e.ActorID = id.UserID
	e.SupportMode = inMode
	if inMode {
		e.ActorID = mode.AgentUserID
	}
	if e.OrgID == 0 {
		e.OrgID = id.OrgID
	}
	e.CreatedAt = s.nowUnix()
	e.Detail = truncate(clean(e.Detail), 500)
	return s.store.InsertAudit(ctx, e)
}

// notify delivers one support event, tolerating an installation with no
// notification system configured.
func (s *Service) notify(ctx context.Context, event string, userID int64, fields map[string]string) {
	if s.notifier == nil || userID == 0 {
		return
	}
	if fields == nil {
		fields = map[string]string{}
	}
	fields["site"] = s.opts.SiteName
	if err := s.notifier.Notify(ctx, event, userID, fields); err != nil {
		s.logger().Warn("support notification failed",
			slog.String("event", event),
			slog.String("error_code", errors.CodeOf(err)))
	}
}

// allow applies a rate limit, returning a ready-made error when the caller has
// run out of allowance.
func (s *Service) allow(name string, id Identity) error {
	key := id.RemoteKey
	if id.UserID != 0 {
		key = strconv.FormatInt(id.UserID, 10)
	}
	if key == "" {
		key = "anonymous"
	}
	ok, retryAfter := s.limits.Allow(name, key)
	if ok {
		return nil
	}
	return errors.New(errors.CodeRateLimited, 429, "Too many requests").
		WithDetails(map[string]any{"retry_after_seconds": int(retryAfter.Seconds())})
}
