package billing

// Domain types for the billing subsystem. Every field holding money is an
// int64 count of the currency's minor unit and is named *Minor; every rate is
// basis points and is named *BPS; every timestamp is Unix seconds in UTC.

// Account is a tenant's billing profile. It carries no card data: the only
// payment references it holds are provider tokens on PaymentMethod.
type Account struct {
	ID                      string `json:"id"`
	TenantID                string `json:"tenant_id"`
	Currency                string `json:"currency"`
	BillingEmail            string `json:"billing_email"`
	LegalName               string `json:"legal_name"`
	AddressLine1            string `json:"address_line1"`
	AddressLine2            string `json:"address_line2"`
	City                    string `json:"city"`
	Region                  string `json:"region"`
	PostalCode              string `json:"postal_code"`
	Country                 string `json:"country"`
	TaxID                   string `json:"tax_id"`
	TaxIDStatus             string `json:"tax_id_status"`
	IsBusiness              bool   `json:"is_business"`
	BalanceMinor            int64  `json:"balance_minor"`
	AutoRecharge            bool   `json:"auto_recharge"`
	AutoRechargeThreshold   int64  `json:"auto_recharge_threshold_minor"`
	AutoRechargeAmountMinor int64  `json:"auto_recharge_amount_minor"`
	DefaultMethodID         string `json:"default_method_id"`
	CreatedAt               int64  `json:"created_at"`
	UpdatedAt               int64  `json:"updated_at"`
	Version                 int64  `json:"-"`
}

// Plan is a priced tier. A plan may only cap how much of a resource a tenant
// consumes; it can never remove a product feature, because cashp ships every
// feature to every tenant on every tier (IDEA.md non-goals).
type Plan struct {
	ID            string      `json:"id"`
	Code          string      `json:"code"`
	Name          string      `json:"name"`
	Description   string      `json:"description"`
	Currency      string      `json:"currency"`
	PriceMinor    int64       `json:"price_minor"`
	Cycle         string      `json:"cycle"`
	TrialDays     int64       `json:"trial_days"`
	GraceDays     int64       `json:"grace_days"`
	Visibility    string      `json:"visibility"`
	OveragePolicy string      `json:"overage_policy"`
	Active        bool        `json:"active"`
	SortOrder     int64       `json:"sort_order"`
	Quotas        []PlanQuota `json:"quotas,omitempty"`
	CreatedAt     int64       `json:"created_at"`
	UpdatedAt     int64       `json:"updated_at"`
	Version       int64       `json:"-"`
}

// PlanQuota is one resource ceiling belonging to a plan. LimitValue is
// Unlimited (-1) for no ceiling and 0 for "none allowed".
type PlanQuota struct {
	ID                    string `json:"id"`
	PlanID                string `json:"plan_id"`
	Resource              string `json:"resource"`
	LimitValue            int64  `json:"limit_value"`
	Enforcement           string `json:"enforcement"`
	BurstValue            int64  `json:"burst_value"`
	OverageUnitPriceMinor int64  `json:"overage_unit_price_minor"`
	UpdatedAt             int64  `json:"updated_at"`
}

// QuotaOverride raises or lowers a single ceiling for a single tenant. Only a
// global admin may create one and every creation is audit-logged.
type QuotaOverride struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	Resource    string `json:"resource"`
	LimitValue  int64  `json:"limit_value"`
	Enforcement string `json:"enforcement"`
	Reason      string `json:"reason"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   int64  `json:"created_at"`
	ExpiresAt   int64  `json:"expires_at"`
}

// Subscription binds a tenant to a plan for a billing period.
type Subscription struct {
	ID                 string `json:"id"`
	TenantID           string `json:"tenant_id"`
	AccountID          string `json:"account_id"`
	PlanID             string `json:"plan_id"`
	State              string `json:"state"`
	Cycle              string `json:"cycle"`
	Currency           string `json:"currency"`
	PriceMinor         int64  `json:"price_minor"`
	Quantity           int64  `json:"quantity"`
	TrialEndsAt        int64  `json:"trial_ends_at"`
	PeriodStart        int64  `json:"period_start"`
	PeriodEnd          int64  `json:"period_end"`
	GraceEndsAt        int64  `json:"grace_ends_at"`
	CancelAtPeriodEnd  bool   `json:"cancel_at_period_end"`
	CancelledAt        int64  `json:"cancelled_at"`
	EndedAt            int64  `json:"ended_at"`
	PendingPlanID      string `json:"pending_plan_id"`
	PendingEffectiveAt int64  `json:"pending_effective_at"`
	Provider           string `json:"provider"`
	ProviderRef        string `json:"-"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
	Version            int64  `json:"-"`
}

// Active reports whether the subscription currently entitles the tenant to
// its plan quotas. Grace and trial both count as entitled: cashp never
// terminates service the moment a charge fails.
func (s Subscription) Active() bool {
	switch s.State {
	case StateTrialing, StateActive, StatePastDue:
		return true
	case StateCancelled:
		return s.EndedAt == 0
	default:
		return false
	}
}

// SubscriptionEvent is one append-only lifecycle record.
type SubscriptionEvent struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	SubscriptionID string `json:"subscription_id"`
	Event          string `json:"event"`
	FromState      string `json:"from_state"`
	ToState        string `json:"to_state"`
	Actor          string `json:"actor"`
	Detail         string `json:"detail"`
	OccurredAt     int64  `json:"occurred_at"`
}

// Meter defines something the platform measures for a tenant.
type Meter struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	MeterType   string `json:"meter_type"`
	Resource    string `json:"resource"`
	Unit        string `json:"unit"`
	ResetPolicy string `json:"reset_policy"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// UsageRecord is one measurement for one tenant in one aggregation period.
type UsageRecord struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	MeterCode   string `json:"meter_code"`
	PeriodStart int64  `json:"period_start"`
	PeriodEnd   int64  `json:"period_end"`
	Value       int64  `json:"value"`
	Samples     int64  `json:"samples"`
	State       string `json:"state"`
	Dimensions  string `json:"dimensions"`
	RecordedAt  int64  `json:"recorded_at"`
}

// UsageRollup is the billing-period total the invoice generator reads.
type UsageRollup struct {
	ID            string `json:"id"`
	TenantID      string `json:"tenant_id"`
	MeterCode     string `json:"meter_code"`
	PeriodStart   int64  `json:"period_start"`
	PeriodEnd     int64  `json:"period_end"`
	TotalValue    int64  `json:"total_value"`
	IncludedValue int64  `json:"included_value"`
	OverageValue  int64  `json:"overage_value"`
	Invoiced      bool   `json:"invoiced"`
	RolledAt      int64  `json:"rolled_at"`
}

// Invoice is immutable once issued. Every later change is a credit note.
type Invoice struct {
	ID              string        `json:"id"`
	TenantID        string        `json:"tenant_id"`
	AccountID       string        `json:"account_id"`
	SubscriptionID  string        `json:"subscription_id"`
	Number          string        `json:"number"`
	State           string        `json:"state"`
	Currency        string        `json:"currency"`
	SubtotalMinor   int64         `json:"subtotal_minor"`
	DiscountMinor   int64         `json:"discount_minor"`
	TaxMinor        int64         `json:"tax_minor"`
	TotalMinor      int64         `json:"total_minor"`
	PaidMinor       int64         `json:"paid_minor"`
	CreditedMinor   int64         `json:"credited_minor"`
	TaxRateBPS      int64         `json:"tax_rate_bps"`
	TaxKind         string        `json:"tax_kind"`
	TaxJurisdiction string        `json:"tax_jurisdiction"`
	ReverseCharge   bool          `json:"reverse_charge"`
	PeriodStart     int64         `json:"period_start"`
	PeriodEnd       int64         `json:"period_end"`
	IssuedAt        int64         `json:"issued_at"`
	DueAt           int64         `json:"due_at"`
	PaidAt          int64         `json:"paid_at"`
	VoidedAt        int64         `json:"voided_at"`
	BuyerSnapshot   string        `json:"buyer_snapshot"`
	Notes           string        `json:"notes"`
	Lines           []InvoiceLine `json:"lines,omitempty"`
	CreatedAt       int64         `json:"created_at"`
	UpdatedAt       int64         `json:"updated_at"`
	Version         int64         `json:"-"`
}

// BalanceDueMinor is what is still owed after payments and credit notes.
func (i Invoice) BalanceDueMinor() int64 {
	return ClampNonNegative(i.TotalMinor - i.PaidMinor - i.CreditedMinor)
}

// InvoiceLine is one billed item. QuantityMilli is the quantity times 1000 so
// a fractional quantity such as 1.5 GB-months stays an integer.
type InvoiceLine struct {
	ID              string `json:"id"`
	InvoiceID       string `json:"invoice_id"`
	TenantID        string `json:"tenant_id"`
	Position        int64  `json:"position"`
	Kind            string `json:"kind"`
	Description     string `json:"description"`
	QuantityMilli   int64  `json:"quantity_milli"`
	UnitPriceMinor  int64  `json:"unit_price_minor"`
	AmountMinor     int64  `json:"amount_minor"`
	TaxRateBPS      int64  `json:"tax_rate_bps"`
	TaxMinor        int64  `json:"tax_minor"`
	MeterCode       string `json:"meter_code"`
	LinePeriodStart int64  `json:"period_start"`
	LinePeriodEnd   int64  `json:"period_end"`
}

// CreditNote adjusts an issued invoice without mutating it.
type CreditNote struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	InvoiceID   string `json:"invoice_id"`
	Number      string `json:"number"`
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
	TaxMinor    int64  `json:"tax_minor"`
	Reason      string `json:"reason"`
	Note        string `json:"note"`
	IssuedAt    int64  `json:"issued_at"`
	CreatedBy   string `json:"created_by"`
}

// PaymentMethod is a tokenized instrument. provider_token is opaque to cashp
// and the only card-derived values kept are the display fields the provider
// itself hands back.
type PaymentMethod struct {
	ID               string `json:"id"`
	TenantID         string `json:"tenant_id"`
	AccountID        string `json:"account_id"`
	Provider         string `json:"provider"`
	ProviderToken    string `json:"-"`
	ProviderCustomer string `json:"-"`
	Kind             string `json:"kind"`
	Brand            string `json:"brand"`
	Last4            string `json:"last4"`
	ExpMonth         int64  `json:"exp_month"`
	ExpYear          int64  `json:"exp_year"`
	HolderName       string `json:"holder_name"`
	Country          string `json:"country"`
	IsDefault        bool   `json:"is_default"`
	State            string `json:"state"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

// PaymentAttempt records one charge try. IdempotencyKey is unique in the
// database so a replayed request returns the original outcome instead of
// charging a second time.
type PaymentAttempt struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	AccountID      string `json:"account_id"`
	InvoiceID      string `json:"invoice_id"`
	MethodID       string `json:"method_id"`
	Provider       string `json:"provider"`
	IdempotencyKey string `json:"idempotency_key"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	State          string `json:"state"`
	ProviderRef    string `json:"provider_ref"`
	FailureCode    string `json:"failure_code"`
	FailureMessage string `json:"failure_message"`
	AttemptNumber  int64  `json:"attempt_number"`
	AttemptedAt    int64  `json:"attempted_at"`
	CompletedAt    int64  `json:"completed_at"`
}

// Refund is money returned to a payer, always paired with a credit note.
type Refund struct {
	ID           string `json:"id"`
	TenantID     string `json:"tenant_id"`
	InvoiceID    string `json:"invoice_id"`
	AttemptID    string `json:"attempt_id"`
	CreditNoteID string `json:"credit_note_id"`
	Provider     string `json:"provider"`
	ProviderRef  string `json:"provider_ref"`
	AmountMinor  int64  `json:"amount_minor"`
	Currency     string `json:"currency"`
	Kind         string `json:"kind"`
	State        string `json:"state"`
	Reason       string `json:"reason"`
	CreatedAt    int64  `json:"created_at"`
	CompletedAt  int64  `json:"completed_at"`
}

// ProviderRecord is the stored configuration of one payment provider. The
// credential blobs are AES-256-GCM ciphertext and never leave this struct in
// plaintext: the API and UI see ProviderView instead.
type ProviderRecord struct {
	Name               string `json:"name"`
	DisplayName        string `json:"display_name"`
	Category           string `json:"category"`
	Enabled            bool   `json:"enabled"`
	TestMode           bool   `json:"test_mode"`
	State              string `json:"state"`
	Priority           int64  `json:"priority"`
	CredentialsEnc     []byte `json:"-"`
	CredentialsTestEnc []byte `json:"-"`
	HealthState        string `json:"health_state"`
	HealthDetail       string `json:"health_detail"`
	HealthCheckedAt    int64  `json:"health_checked_at"`
	ConfiguredAt       int64  `json:"configured_at"`
	ConfiguredBy       string `json:"configured_by"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
	Version            int64  `json:"-"`
}

// ProviderView is the outward-facing shape of a provider. Credential values
// are replaced by masked previews so an API response or an admin page can
// never leak a secret key.
type ProviderView struct {
	Name            string            `json:"name"`
	DisplayName     string            `json:"display_name"`
	Category        string            `json:"category"`
	Enabled         bool              `json:"enabled"`
	TestMode        bool              `json:"test_mode"`
	State           string            `json:"state"`
	Priority        int64             `json:"priority"`
	HealthState     string            `json:"health_state"`
	HealthDetail    string            `json:"health_detail"`
	HealthCheckedAt int64             `json:"health_checked_at"`
	ConfiguredAt    int64             `json:"configured_at"`
	Configured      bool              `json:"configured"`
	Credentials     map[string]string `json:"credentials"`
	Fields          []CredentialField `json:"fields"`
}

// CredentialField describes one configurable provider credential together
// with the help text the admin UI shows next to its input.
type CredentialField struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"`
	Placeholder string `json:"placeholder"`
	// Tooltip explains what the value is, where in the provider's dashboard
	// to find it, and what format it takes. Every field carries one.
	Tooltip string `json:"tooltip"`
}

// WebhookEvent is the idempotency ledger entry for one inbound provider
// delivery.
type WebhookEvent struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	EventID     string `json:"event_id"`
	EventType   string `json:"event_type"`
	State       string `json:"state"`
	PayloadHash string `json:"payload_hash"`
	Detail      string `json:"detail"`
	ReceivedAt  int64  `json:"received_at"`
	ProcessedAt int64  `json:"processed_at"`
}

// DunningState tracks retry progress for a subscription in arrears.
type DunningState struct {
	SubscriptionID string `json:"subscription_id"`
	TenantID       string `json:"tenant_id"`
	InvoiceID      string `json:"invoice_id"`
	State          string `json:"state"`
	Attempt        int64  `json:"attempt"`
	NextAttemptAt  int64  `json:"next_attempt_at"`
	StartedAt      int64  `json:"started_at"`
	LastError      string `json:"last_error"`
	UpdatedAt      int64  `json:"updated_at"`
}

// TaxRate is one operator-maintained jurisdiction rate.
type TaxRate struct {
	ID               string `json:"id"`
	Country          string `json:"country"`
	Region           string `json:"region"`
	Kind             string `json:"kind"`
	Name             string `json:"name"`
	RateBPS          int64  `json:"rate_bps"`
	ReverseChargeB2B bool   `json:"reverse_charge_b2b"`
	Active           bool   `json:"active"`
	UpdatedAt        int64  `json:"updated_at"`
}

// AuditEntry is one append-only financial audit record.
type AuditEntry struct {
	ID         string `json:"id"`
	OccurredAt int64  `json:"occurred_at"`
	TenantID   string `json:"tenant_id"`
	Actor      string `json:"actor"`
	Action     string `json:"action"`
	Target     string `json:"target"`
	Provider   string `json:"provider"`
	Result     string `json:"result"`
	Code       string `json:"code"`
	IP         string `json:"ip"`
	Detail     string `json:"detail"`
}

// QuotaStatus is one resource's ceiling and current consumption, as shown on
// the tenant's usage dashboard and returned by the quota API.
type QuotaStatus struct {
	Resource    string `json:"resource"`
	LimitValue  int64  `json:"limit_value"`
	Used        int64  `json:"used"`
	Remaining   int64  `json:"remaining"`
	Enforcement string `json:"enforcement"`
	Unlimited   bool   `json:"unlimited"`
	Source      string `json:"source"`
	Unit        string `json:"unit"`
}
