package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/logging"
)

// Settings keys stored in billing_settings. Provider credentials are never
// among them: those live encrypted on billing_providers and nowhere else.
const (
	SettingBaseCurrency    = "base_currency"
	SettingInvoicePrefix   = "invoice_prefix"
	SettingCreditPrefix    = "credit_prefix"
	SettingGraceDays       = "grace_period_days"
	SettingRetrySchedule   = "retry_schedule_days"
	SettingProviderTimeout = "provider_timeout_seconds"
	SettingFailoverMode    = "failover_mode"
	SettingTaxEnabled      = "tax_enabled"
	SettingDueDays         = "invoice_due_days"
	SettingBillingEnabled  = "billing_enabled"
	SettingSellerName      = "seller_name"
	SettingSellerAddress   = "seller_address"
	SettingSellerTaxID     = "seller_tax_id"
)

// SettingDiscrepancyThreshold is how many reconciliation mismatches in a
// single day trigger an operator alert rather than a quiet log line.
const SettingDiscrepancyThreshold = "reconcile_discrepancy_threshold"

// Defaults for every operator-tunable billing setting.
const (
	DefaultInvoicePrefix    = "INV-"
	DefaultCreditPrefix     = "CN-"
	DefaultGraceDays        = 7
	DefaultDueDays          = 14
	DefaultProviderTimeout  = 30 * time.Second
	DefaultFailoverMode     = "automatic"
	DefaultRetryScheduleRaw = "1,3,5,7"
)

// FailoverManual keeps a failed charge on its original provider instead of
// walking down the priority chain.
const FailoverManual = "manual"

// CounterFunc reports how much of a resource a tenant is currently using.
// Other subsystems register one per resource they own so the quota engine
// never has to import them, which is what keeps billing free of dependencies
// on the packages it gates.
type CounterFunc func(ctx context.Context, tenantID string) (int64, error)

// Notifier delivers a billing notification. The notifications subsystem
// supplies the implementation; billing works fine without one, so every send
// is nil-guarded and a delivery failure never blocks a financial operation.
type Notifier interface {
	Notify(ctx context.Context, tenantID, event string, data map[string]any) error
}

// Renderer renders one of this package's own HTML templates. The server
// injects its renderer at wiring time.
type Renderer interface {
	Render(w http.ResponseWriter, r *http.Request, name string, data any) error
}

// Options configures a Service.
type Options struct {
	// DB is the shared database handle. Required.
	DB *database.DB
	// EncryptionKey is the 32-byte AES-256-GCM key that wraps provider
	// credentials at rest. Required before any provider can be configured.
	EncryptionKey []byte
	// Notifier delivers billing notifications; optional.
	Notifier Notifier
	// Renderer renders the billing pages; optional for API-only wiring.
	Renderer Renderer
	// AdminPath is the configured server administration path segment, used
	// to build the admin route table. Never hardcode it.
	AdminPath string
	// Identity resolves the caller of an HTTP request. The server supplies a
	// small adapter over its own authentication so billing never imports the
	// auth package, which is what keeps the two free of an import cycle when
	// auth asks billing for a quota decision. Billing's HTTP routes refuse
	// every request while it is nil, because a billing page with no idea who
	// is asking must not answer.
	Identity IdentityFunc
	// HTTPClient is used for outbound provider calls. A sane client with a
	// timeout is built when nil.
	HTTPClient *http.Client
	// Now overrides the clock, for tests.
	Now func() time.Time
}

// Service is the billing subsystem. One instance is constructed by the
// server and shared by the HTTP handlers, the scheduler tasks and any other
// package that needs a quota decision.
type Service struct {
	db       *database.DB
	key      []byte
	notifier Notifier
	renderer Renderer
	adminPth string
	client   *http.Client
	identity IdentityFunc
	now      func() time.Time

	mu       sync.RWMutex
	counters map[string]CounterFunc
}

// New builds a Service.
func New(opts Options) (*Service, error) {
	if opts.DB == nil {
		return nil, ErrValidation("billing: a database handle is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: DefaultProviderTimeout}
	}
	adminPath := strings.Trim(opts.AdminPath, "/")
	if adminPath == "" {
		adminPath = "administration"
	}
	return &Service{
		db:       opts.DB,
		key:      opts.EncryptionKey,
		notifier: opts.Notifier,
		renderer: opts.Renderer,
		adminPth: adminPath,
		client:   client,
		identity: opts.Identity,
		now:      now,
		counters: make(map[string]CounterFunc),
	}, nil
}

// DB exposes the handle the service was built with, for the tasks and
// handlers defined in this package.
func (s *Service) DB() *database.DB { return s.db }

// AdminPath returns the configured administration path segment.
func (s *Service) AdminPath() string { return s.adminPth }

// Now returns the service clock.
func (s *Service) Now() time.Time { return s.now().UTC() }

// unix is the service clock as Unix seconds, the form every timestamp column
// in this package stores.
func (s *Service) unix() int64 { return s.now().UTC().Unix() }

// RegisterCounter binds a live usage counter to a quota resource. It is how
// the hosting, container, database and mail subsystems tell billing how much
// of their resource a tenant holds without either side importing the other.
func (s *Service) RegisterCounter(resource string, fn CounterFunc) error {
	if !ValidResource(resource) {
		return ErrValidation("billing: unknown quota resource " + resource)
	}
	if fn == nil {
		return ErrValidation("billing: a counter function is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counters[resource] = fn
	return nil
}

// counterFor returns the registered counter for a resource, if any.
func (s *Service) counterFor(resource string) (CounterFunc, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn, ok := s.counters[resource]
	return fn, ok
}

// defaultService is the process-wide instance the package-level helpers use.
// It stays nil on an install with billing unconfigured, and every helper
// treats that as "no billing, therefore no quotas".
var (
	defaultMu sync.RWMutex
	defaultSv *Service
)

// SetDefault publishes a service as the process-wide instance.
func SetDefault(s *Service) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultSv = s
}

// Default returns the process-wide service, or nil when billing is not
// configured on this server.
func Default() *Service {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultSv
}

// newID returns a random lowercase hex identifier for a billing row.
func newID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// A failure of the system CSPRNG is not recoverable and must never
		// silently degrade into a predictable identifier.
		panic("billing: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

// itoa renders an int64 in base ten.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// boolToInt converts a Go bool to the integer form every boolean column in
// this schema stores.
func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Setting reads one billing setting, returning the fallback when it has
// never been set.
func (s *Service) Setting(ctx context.Context, key, fallback string) string {
	var value string
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT setting_value FROM billing_settings WHERE setting_key = ?`, key).Scan(&value)
	if err != nil || value == "" {
		return fallback
	}
	return value
}

// SettingInt reads one billing setting as an integer.
func (s *Service) SettingInt(ctx context.Context, key string, fallback int64) int64 {
	raw := s.Setting(ctx, key, "")
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

// SettingBool reads one billing setting as a boolean.
func (s *Service) SettingBool(ctx context.Context, key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(s.Setting(ctx, key, "")))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

// SetSetting writes one billing setting. Credentials are rejected outright:
// nothing secret is ever stored in this table.
func (s *Service) SetSetting(ctx context.Context, key, value, actor string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return ErrValidation("billing: a setting key is required")
	}
	if isSecretSettingKey(key) {
		return ErrValidation("billing: secrets are not stored in settings; configure them on the provider")
	}
	now := s.unix()
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_settings SET setting_value = ?, updated_at = ?, updated_by = ?
		 WHERE setting_key = ?`, value, now, actor, key)
	if err != nil {
		return ErrInternal(err, "Could not save the billing setting.")
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_settings (setting_key, setting_value, updated_at, updated_by)
		 VALUES (?, ?, ?, ?)`, key, value, now, actor)
	if err != nil {
		return ErrInternal(err, "Could not save the billing setting.")
	}
	return nil
}

// isSecretSettingKey reports whether a settings key looks like a credential.
func isSecretSettingKey(key string) bool {
	lower := strings.ToLower(key)
	for _, part := range []string{"secret", "password", "token", "api_key", "apikey", "private"} {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return false
}

// BaseCurrency returns the operator's configured base currency.
func (s *Service) BaseCurrency(ctx context.Context) string {
	code, err := NormalizeCurrency(s.Setting(ctx, SettingBaseCurrency, DefaultCurrency))
	if err != nil {
		return DefaultCurrency
	}
	return code
}

// Enabled reports whether the operator has switched billing on. A fresh
// install answers false, which is what makes first run work with no billing
// configuration and no quotas applied to anybody.
func (s *Service) Enabled(ctx context.Context) bool {
	return s.SettingBool(ctx, SettingBillingEnabled, false)
}

// notify sends a billing notification, tolerating both an absent notifier
// and a delivery failure. A notification is never allowed to fail a
// financial operation that has already been recorded.
func (s *Service) notify(ctx context.Context, tenantID, event string, data map[string]any) {
	if s.notifier == nil {
		return
	}
	if err := s.notifier.Notify(ctx, tenantID, event, data); err != nil {
		logging.L().Warn("billing notification failed",
			"event", event, "tenant_id", tenantID, "error", err.Error())
	}
}
