package billing

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/billing/provider"
	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/security"
)

// providerColumns is the explicit column list for billing_providers.
const providerColumns = `name, display_name, category, enabled, test_mode, state,
	priority, credentials_enc, credentials_test_enc, health_state, health_detail,
	health_checked_at, configured_at, configured_by, created_at, updated_at, version`

// scanProvider reads one row in providerColumns order.
func scanProvider(sc interface{ Scan(...any) error }) (ProviderRecord, error) {
	var p ProviderRecord
	var enabled, testMode int64
	var credEnc, credTestEnc string
	if err := sc.Scan(&p.Name, &p.DisplayName, &p.Category, &enabled, &testMode,
		&p.State, &p.Priority, &credEnc, &credTestEnc, &p.HealthState,
		&p.HealthDetail, &p.HealthCheckedAt, &p.ConfiguredAt, &p.ConfiguredBy,
		&p.CreatedAt, &p.UpdatedAt, &p.Version); err != nil {
		return ProviderRecord{}, err
	}
	p.Enabled = enabled != 0
	p.TestMode = testMode != 0
	p.CredentialsEnc = []byte(credEnc)
	p.CredentialsTestEnc = []byte(credTestEnc)
	return p, nil
}

// SyncProviders makes sure every compiled-in driver has a registry row. New
// rows are created disabled and in test mode, so upgrading cashp can never
// switch a payment provider on by itself.
func (s *Service) SyncProviders(ctx context.Context) error {
	now := s.unix()
	for _, d := range provider.Drivers() {
		_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
			`INSERT INTO billing_providers
			 (name, display_name, category, enabled, test_mode, state, priority,
			  created_at, updated_at, version)
			 VALUES (?, ?, ?, 0, 1, ?, 100, ?, ?, 1)`,
			d.Name, d.DisplayName, d.Category, ProviderUnconfigured, now, now)
		if err != nil && !database.IsAlreadyExistsError(err) {
			return ErrInternal(err, "Could not register the payment providers.")
		}
	}
	return nil
}

// ProviderByName returns one provider registry row.
func (s *Service) ProviderByName(ctx context.Context, name string) (ProviderRecord, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+providerColumns+` FROM billing_providers WHERE name = ?`, name)
	rec, err := scanProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderRecord{}, ErrNotFound("payment provider")
	}
	if err != nil {
		return ProviderRecord{}, ErrInternal(err, "Could not read the payment provider.")
	}
	return rec, nil
}

// ListProviderRecords returns every registry row in priority order.
func (s *Service) ListProviderRecords(ctx context.Context) ([]ProviderRecord, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+providerColumns+` FROM billing_providers ORDER BY priority, name`)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the payment providers.")
	}
	defer func() { _ = rows.Close() }()

	out := []ProviderRecord{}
	for rows.Next() {
		rec, sErr := scanProvider(rows)
		if sErr != nil {
			return nil, ErrInternal(sErr, "Could not read the payment providers.")
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the payment providers.")
	}
	return out, nil
}

// ListProviders returns the masked, outward-facing view of every provider,
// including its credential field definitions and their help text.
func (s *Service) ListProviders(ctx context.Context) ([]ProviderView, error) {
	records, err := s.ListProviderRecords(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProviderView, 0, len(records))
	for _, rec := range records {
		view, vErr := s.providerView(rec)
		if vErr != nil {
			return nil, vErr
		}
		out = append(out, view)
	}
	return out, nil
}

// EnabledProviderCount returns how many providers are enabled and how many
// are registered, for the "2 of 6 enabled" indicator in the admin interface.
func (s *Service) EnabledProviderCount(ctx context.Context) (int, int, error) {
	records, err := s.ListProviderRecords(ctx)
	if err != nil {
		return 0, 0, err
	}
	enabled := 0
	for _, rec := range records {
		if rec.Enabled {
			enabled++
		}
	}
	return enabled, len(records), nil
}

// DriverInfo is the static help material a provider plugin ships with: the
// step-by-step account setup, the per-field tooltips and the troubleshooting
// notes. The administration screen renders it inline, so configuring a
// provider never requires reading documentation held somewhere else.
type DriverInfo struct {
	Name            string            `json:"name"`
	DisplayName     string            `json:"display_name"`
	Category        string            `json:"category"`
	SetupGuide      []string          `json:"setup_guide"`
	Troubleshooting map[string]string `json:"troubleshooting"`
	Fields          []CredentialField `json:"fields"`
}

// DriverHelp returns the help material for one registered driver.
func DriverHelp(name string) (DriverInfo, error) {
	driver, err := provider.Lookup(name)
	if err != nil {
		return DriverInfo{}, ErrNotFound("payment provider")
	}
	info := DriverInfo{
		Name:            driver.Name,
		DisplayName:     driver.DisplayName,
		Category:        driver.Category,
		SetupGuide:      driver.SetupGuide,
		Troubleshooting: driver.Troubleshooting,
		Fields:          make([]CredentialField, 0, len(driver.Fields)),
	}
	for _, f := range driver.Fields {
		info.Fields = append(info.Fields, CredentialField{
			Name:        f.Name,
			Label:       f.Label,
			Required:    f.Required,
			Secret:      f.Secret,
			Placeholder: f.Placeholder,
			Tooltip:     f.Tooltip,
		})
	}
	return info, nil
}

// providerView masks a registry row for display. A stored secret is never
// returned: the caller sees only a masked preview that confirms something is
// configured, and the driver's own tooltip for each field.
func (s *Service) providerView(rec ProviderRecord) (ProviderView, error) {
	view := ProviderView{
		Name:            rec.Name,
		DisplayName:     rec.DisplayName,
		Category:        rec.Category,
		Enabled:         rec.Enabled,
		TestMode:        rec.TestMode,
		State:           rec.State,
		Priority:        rec.Priority,
		HealthState:     rec.HealthState,
		HealthDetail:    rec.HealthDetail,
		HealthCheckedAt: rec.HealthCheckedAt,
		ConfiguredAt:    rec.ConfiguredAt,
		Credentials:     map[string]string{},
		Fields:          []CredentialField{},
	}
	driver, err := provider.Lookup(rec.Name)
	if err == nil {
		for _, f := range driver.Fields {
			view.Fields = append(view.Fields, CredentialField{
				Name:        f.Name,
				Label:       f.Label,
				Required:    f.Required,
				Secret:      f.Secret,
				Placeholder: f.Placeholder,
				Tooltip:     f.Tooltip,
			})
		}
	}

	creds, err := s.credentials(rec, rec.TestMode)
	if err != nil {
		return view, nil
	}
	view.Configured = len(creds) > 0
	for _, f := range view.Fields {
		value := creds[f.Name]
		if value == "" {
			continue
		}
		if f.Secret {
			view.Credentials[f.Name] = security.MaskSecret(value)
			continue
		}
		view.Credentials[f.Name] = value
	}
	return view, nil
}

// credentials decrypts one credential set. The plaintext exists only for the
// life of the call that needs it and is never written anywhere.
func (s *Service) credentials(rec ProviderRecord, testMode bool) (map[string]string, error) {
	blob := rec.CredentialsEnc
	if testMode {
		blob = rec.CredentialsTestEnc
	}
	if len(blob) == 0 {
		return map[string]string{}, nil
	}
	raw, err := base64.StdEncoding.DecodeString(string(blob))
	if err != nil {
		return nil, ErrInternal(err, "The stored provider credentials could not be read.")
	}
	plain, err := security.Decrypt(s.key, raw)
	if err != nil {
		return nil, ErrInternal(err, "The stored provider credentials could not be decrypted.")
	}
	out := map[string]string{}
	if err := json.Unmarshal(plain, &out); err != nil {
		return nil, ErrInternal(err, "The stored provider credentials could not be read.")
	}
	return out, nil
}

// sealCredentials encrypts a credential set for storage.
func (s *Service) sealCredentials(creds map[string]string) (string, error) {
	if len(creds) == 0 {
		return "", nil
	}
	plain, err := json.Marshal(creds)
	if err != nil {
		return "", ErrInternal(err, "Could not store the provider credentials.")
	}
	sealed, err := security.Encrypt(s.key, plain)
	if err != nil {
		return "", ErrInternal(err, "Could not encrypt the provider credentials.")
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// ConfigureProvider stores a provider's credentials. Test and live
// credentials live in separate columns and are never interchanged, so a
// sandbox test can never run against a live key. Storing credentials does not
// enable the provider.
func (s *Service) ConfigureProvider(ctx context.Context, name string, creds map[string]string, testMode bool, actor, ip string) (ProviderView, error) {
	rec, err := s.ProviderByName(ctx, name)
	if err != nil {
		return ProviderView{}, err
	}
	driver, err := provider.Lookup(name)
	if err != nil {
		return ProviderView{}, ErrNotFound("payment provider")
	}

	// An omitted secret keeps whatever is already stored, so an administrator
	// editing one field does not have to retype every key.
	existing, err := s.credentials(rec, testMode)
	if err != nil {
		return ProviderView{}, err
	}
	merged := map[string]string{}
	for k, v := range existing {
		merged[k] = v
	}
	for _, f := range driver.Fields {
		value, present := creds[f.Name]
		if !present {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" && f.Secret {
			continue
		}
		merged[f.Name] = value
	}
	if err := driver.Validate(merged); err != nil {
		return ProviderView{}, ErrValidation(err.Error())
	}

	sealed, err := s.sealCredentials(merged)
	if err != nil {
		return ProviderView{}, err
	}
	column := "credentials_enc"
	if testMode {
		column = "credentials_test_enc"
	}
	now := s.unix()
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_providers SET `+column+` = ?, test_mode = ?, state = ?,
		   configured_at = ?, configured_by = ?, updated_at = ?, version = version + 1
		 WHERE name = ? AND version = ?`,
		sealed, boolToInt(testMode), ProviderTesting, now, actor, now,
		name, rec.Version); err != nil {
		return ProviderView{}, providerWriteError(err)
	}

	s.WriteAudit(ctx, AuditRecord{
		Actor: actor, Action: ActionProviderConfigured, Target: "provider:" + name,
		Provider: name, IP: ip, Detail: "test_mode=" + boolText(testMode),
	})
	return s.ProviderView(ctx, name)
}

// ProviderView returns one masked provider view.
func (s *Service) ProviderView(ctx context.Context, name string) (ProviderView, error) {
	rec, err := s.ProviderByName(ctx, name)
	if err != nil {
		return ProviderView{}, err
	}
	return s.providerView(rec)
}

// SetProviderEnabled turns a provider on or off. A provider can only be
// enabled once its credentials validate against the provider itself, so a
// misconfigured gateway can never become the one billing tries to charge.
func (s *Service) SetProviderEnabled(ctx context.Context, name string, enabled bool, actor, ip string) (ProviderView, error) {
	rec, err := s.ProviderByName(ctx, name)
	if err != nil {
		return ProviderView{}, err
	}
	state := ProviderDisabled
	action := ActionProviderDisabled
	if enabled {
		if _, err := s.TestProvider(ctx, name, actor, ip); err != nil {
			return ProviderView{}, err
		}
		state = ProviderActive
		action = ActionProviderEnabled
		rec, err = s.ProviderByName(ctx, name)
		if err != nil {
			return ProviderView{}, err
		}
	}
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_providers SET enabled = ?, state = ?, updated_at = ?,
		   version = version + 1
		 WHERE name = ? AND version = ?`,
		boolToInt(enabled), state, s.unix(), name, rec.Version); err != nil {
		return ProviderView{}, providerWriteError(err)
	}
	s.WriteAudit(ctx, AuditRecord{
		Actor: actor, Action: action, Target: "provider:" + name,
		Provider: name, IP: ip,
	})
	return s.ProviderView(ctx, name)
}

// SetProviderTestMode switches a provider between its sandbox and live
// credentials. Going live is an explicit action and is audited: cashp never
// promotes a provider out of test mode on its own.
func (s *Service) SetProviderTestMode(ctx context.Context, name string, testMode bool, actor, ip string) (ProviderView, error) {
	rec, err := s.ProviderByName(ctx, name)
	if err != nil {
		return ProviderView{}, err
	}
	creds, err := s.credentials(rec, testMode)
	if err != nil {
		return ProviderView{}, err
	}
	if len(creds) == 0 {
		mode := "live"
		if testMode {
			mode = "test"
		}
		return ProviderView{}, ErrValidation("No " + mode + " credentials are stored for this provider yet.")
	}
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_providers SET test_mode = ?, enabled = 0, state = ?,
		   health_state = ?, updated_at = ?, version = version + 1
		 WHERE name = ? AND version = ?`,
		boolToInt(testMode), ProviderTesting, HealthUnknown, s.unix(),
		name, rec.Version); err != nil {
		return ProviderView{}, providerWriteError(err)
	}
	s.WriteAudit(ctx, AuditRecord{
		Actor: actor, Action: ActionProviderConfigured, Target: "provider:" + name,
		Provider: name, IP: ip,
		Detail: "test_mode=" + boolText(testMode) + " disabled pending retest",
	})
	return s.ProviderView(ctx, name)
}

// SetProviderPriority reorders the failover chain. Lower numbers are tried
// first.
func (s *Service) SetProviderPriority(ctx context.Context, name string, priority int64, actor, ip string) error {
	rec, err := s.ProviderByName(ctx, name)
	if err != nil {
		return err
	}
	if priority < 0 || priority > 1000 {
		return ErrValidation("A provider priority is between 0 and 1000.")
	}
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE billing_providers SET priority = ?, updated_at = ?, version = version + 1
		 WHERE name = ? AND version = ?`,
		priority, s.unix(), name, rec.Version); err != nil {
		return providerWriteError(err)
	}
	s.WriteAudit(ctx, AuditRecord{
		Actor: actor, Action: ActionProviderConfigured, Target: "provider:" + name,
		Provider: name, IP: ip, Detail: "priority=" + itoa(priority),
	})
	return nil
}

// ProviderTest is the itemized result of a test-connection run, rendered as a
// checklist next to the provider's configuration form.
type ProviderTest struct {
	Provider        string            `json:"provider"`
	TestMode        bool              `json:"test_mode"`
	Credentials     bool              `json:"credentials_ok"`
	WebhookSecret   bool              `json:"webhook_secret_present"`
	Reachable       bool              `json:"reachable"`
	Detail          string            `json:"detail"`
	CheckedAt       int64             `json:"checked_at"`
	Troubleshooting map[string]string `json:"troubleshooting"`
}

// TestProvider validates a provider's stored credentials against the provider
// itself. In test mode the check runs against the sandbox credentials, so no
// live key is ever exercised by a test.
func (s *Service) TestProvider(ctx context.Context, name, actor, ip string) (ProviderTest, error) {
	rec, err := s.ProviderByName(ctx, name)
	if err != nil {
		return ProviderTest{}, err
	}
	driver, err := provider.Lookup(name)
	if err != nil {
		return ProviderTest{}, ErrNotFound("payment provider")
	}
	result := ProviderTest{
		Provider:        name,
		TestMode:        rec.TestMode,
		CheckedAt:       s.unix(),
		Troubleshooting: driver.Troubleshooting,
	}

	instance, creds, err := s.instance(ctx, rec)
	if err != nil {
		result.Detail = err.Error()
		s.recordProviderHealth(ctx, rec, HealthUnhealthy, result.Detail)
		return result, err
	}
	result.Credentials = true
	result.WebhookSecret = creds["webhook_secret"] != ""

	timeout := time.Duration(s.SettingInt(ctx, SettingProviderTimeout, int64(DefaultProviderTimeout/time.Second))) * time.Second
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := instance.ValidateCredentials(callCtx); err != nil {
		result.Detail = err.Error()
		s.recordProviderHealth(ctx, rec, HealthUnhealthy, result.Detail)
		s.WriteAudit(ctx, AuditRecord{
			Actor: actor, Action: ActionProviderTested, Target: "provider:" + name,
			Provider: name, IP: ip, Result: ResultFailure, Detail: result.Detail,
		})
		return result, ErrUpstream(name, err)
	}

	result.Reachable = true
	result.Detail = "The provider accepted the stored credentials."
	s.recordProviderHealth(ctx, rec, HealthHealthy, result.Detail)
	s.WriteAudit(ctx, AuditRecord{
		Actor: actor, Action: ActionProviderTested, Target: "provider:" + name,
		Provider: name, IP: ip, Result: ResultSuccess,
	})
	return result, nil
}

// instance builds a live driver from a registry row, decrypting credentials
// for this call only.
func (s *Service) instance(ctx context.Context, rec ProviderRecord) (provider.Provider, map[string]string, error) {
	driver, err := provider.Lookup(rec.Name)
	if err != nil {
		return nil, nil, ErrNotFound("payment provider")
	}
	creds, err := s.credentials(rec, rec.TestMode)
	if err != nil {
		return nil, nil, err
	}
	if len(creds) == 0 {
		return nil, nil, ErrProviderDisabled(rec.Name)
	}
	timeout := time.Duration(s.SettingInt(ctx, SettingProviderTimeout, int64(DefaultProviderTimeout/time.Second))) * time.Second
	instance, err := driver.New(provider.Config{
		Credentials: creds,
		TestMode:    rec.TestMode,
		HTTPClient:  s.client,
		Timeout:     timeout,
	})
	if err != nil {
		return nil, nil, ErrValidation(err.Error())
	}
	return instance, creds, nil
}

// ProviderInstance returns a usable driver for an enabled provider. A
// disabled or unconfigured provider is refused here, which is what keeps
// unenabled provider code from ever executing.
func (s *Service) ProviderInstance(ctx context.Context, name string) (provider.Provider, error) {
	rec, err := s.ProviderByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if !rec.Enabled {
		return nil, ErrProviderDisabled(name)
	}
	instance, _, err := s.instance(ctx, rec)
	return instance, err
}

// WebhookSecret returns the signing secret for one provider, decrypted for
// the caller that is about to verify a delivery.
func (s *Service) WebhookSecret(ctx context.Context, name string) (string, error) {
	rec, err := s.ProviderByName(ctx, name)
	if err != nil {
		return "", err
	}
	creds, err := s.credentials(rec, rec.TestMode)
	if err != nil {
		return "", err
	}
	secret := creds["webhook_secret"]
	if secret == "" {
		return "", ErrProviderDisabled(name)
	}
	return secret, nil
}

// EnabledProviders returns the enabled providers in failover order: priority
// first, then healthy before degraded, so a gateway that is currently failing
// is tried last rather than not at all.
func (s *Service) EnabledProviders(ctx context.Context) ([]ProviderRecord, error) {
	records, err := s.ListProviderRecords(ctx)
	if err != nil {
		return nil, err
	}
	out := []ProviderRecord{}
	for _, rec := range records {
		if rec.Enabled {
			out = append(out, rec)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if healthRank(out[i].HealthState) != healthRank(out[j].HealthState) {
			return healthRank(out[i].HealthState) < healthRank(out[j].HealthState)
		}
		return out[i].Priority < out[j].Priority
	})
	return out, nil
}

// healthRank orders health states from most to least preferred.
func healthRank(state string) int {
	switch state {
	case HealthHealthy:
		return 0
	case HealthUnknown:
		return 1
	case HealthDegraded:
		return 2
	default:
		return 3
	}
}

// recordProviderHealth stores the outcome of a health or test check. Only
// enabled or explicitly tested providers are ever checked: cashp does not
// reach out to a provider an administrator has not configured.
func (s *Service) recordProviderHealth(ctx context.Context, rec ProviderRecord, state, detail string) {
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_providers SET health_state = ?, health_detail = ?,
		   health_checked_at = ?, updated_at = ?
		 WHERE name = ?`,
		state, security.MaskSecret(detail), s.unix(), s.unix(), rec.Name); err != nil {
		s.WriteAudit(ctx, AuditRecord{
			Action: ActionProviderTested, Target: "provider:" + rec.Name,
			Provider: rec.Name, Result: ResultFailure,
			Detail: "health write failed: " + err.Error(),
		})
	}
}

// CheckProviderHealth probes every enabled provider and records the result.
func (s *Service) CheckProviderHealth(ctx context.Context) error {
	enabled, err := s.EnabledProviders(ctx)
	if err != nil {
		return err
	}
	for _, rec := range enabled {
		instance, _, iErr := s.instance(ctx, rec)
		if iErr != nil {
			s.recordProviderHealth(ctx, rec, HealthUnhealthy, iErr.Error())
			continue
		}
		timeout := time.Duration(s.SettingInt(ctx, SettingProviderTimeout, int64(DefaultProviderTimeout/time.Second))) * time.Second
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		vErr := instance.ValidateCredentials(callCtx)
		cancel()
		if vErr != nil {
			s.recordProviderHealth(ctx, rec, HealthDegraded, vErr.Error())
			continue
		}
		s.recordProviderHealth(ctx, rec, HealthHealthy, "")
	}
	return nil
}

// providerWriteError renders a failed provider registry write.
func providerWriteError(err error) error {
	if database.IsConflict(err) {
		return ErrConflict("The provider configuration changed while you were editing it; reload and try again.")
	}
	return ErrInternal(err, "Could not update the payment provider.")
}
