// Package provider defines the payment provider abstraction that cashp's
// billing subsystem talks to. Billing never speaks a provider's protocol
// directly: it holds a Provider, and a driver package translates.
//
// Every driver ships disabled. A driver becomes reachable only after an
// administrator configures its credentials and switches it on in the admin
// interface, and a cashp install with no driver enabled is fully functional —
// invoices are still raised and can be settled by hand.
package provider

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Provider categories, used to group drivers in the admin interface.
const (
	CategoryGlobal     = "global"
	CategoryRegional   = "regional"
	CategoryCrypto     = "crypto"
	CategoryBNPL       = "bnpl"
	CategoryEnterprise = "enterprise"
	CategoryBanking    = "banking"
	CategoryWallet     = "wallet"
	CategoryOther      = "other"
)

// ErrUnsupported is returned by a driver for an operation it does not
// implement, such as provider-side subscriptions on a charge-only gateway.
var ErrUnsupported = errors.New("provider: operation is not supported by this provider")

// ErrNotRegistered is returned when no driver is compiled in under a name.
var ErrNotRegistered = errors.New("provider: no such payment provider")

// Field describes one credential input the admin interface renders. Tooltip
// is mandatory: every field explains itself in place, so configuring a
// provider never requires leaving the page to read separate documentation.
type Field struct {
	Name        string
	Label       string
	Required    bool
	Secret      bool
	Placeholder string
	Tooltip     string
}

// Config is what a driver is constructed with. Credentials come from the
// database, decrypted for this call only; they are never read from the
// environment, a config file or source code.
type Config struct {
	Credentials map[string]string
	TestMode    bool
	HTTPClient  *http.Client
	Timeout     time.Duration
	WebhookURL  string
}

// Client returns the HTTP client a driver should use.
func (c Config) Client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

// Capabilities declares what a driver can do, so billing can pick a provider
// that supports the operation it needs instead of discovering a failure.
type Capabilities struct {
	Charge               bool
	Authorize            bool
	Refund               bool
	StoreMethod          bool
	ProviderSubscription bool
	Webhooks             bool
	Currencies           []string
}

// Supports reports whether a currency is accepted. An empty currency list
// means the driver accepts whatever the account is denominated in.
func (c Capabilities) Supports(currency string) bool {
	if len(c.Currencies) == 0 {
		return true
	}
	for _, cur := range c.Currencies {
		if cur == currency {
			return true
		}
	}
	return false
}

// MethodRequest asks a provider to turn a client-side token into a stored
// instrument. cashp never sees a card number: the token is produced in the
// browser by the provider's own element and is all that reaches this server.
type MethodRequest struct {
	TenantID     string
	CustomerRef  string
	Token        string
	HolderName   string
	BillingEmail string
	Country      string
}

// Method is the stored instrument a provider hands back. Only display fields
// are ever returned: no PAN, no CVV, no expiry beyond month and year.
type Method struct {
	Token       string
	CustomerRef string
	Kind        string
	Brand       string
	Last4       string
	ExpMonth    int64
	ExpYear     int64
	Country     string
}

// ChargeRequest is one charge attempt in integer minor units.
type ChargeRequest struct {
	IdempotencyKey string
	AmountMinor    int64
	Currency       string
	MethodToken    string
	CustomerRef    string
	Description    string
	InvoiceNumber  string
	Capture        bool
}

// ChargeResult is the outcome of a charge attempt.
type ChargeResult struct {
	Reference      string
	State          string
	AmountMinor    int64
	Currency       string
	FailureCode    string
	FailureMessage string
	RequiresAction bool
	ActionURL      string
}

// Charge outcome states, mirrored into billing's payment attempt states.
const (
	StatePending    = "pending"
	StateAuthorized = "authorized"
	StateSucceeded  = "succeeded"
	StateFailed     = "failed"
)

// RefundRequest asks for money to be returned to the payer.
type RefundRequest struct {
	IdempotencyKey string
	Reference      string
	AmountMinor    int64
	Currency       string
	Reason         string
}

// RefundResult is the outcome of a refund.
type RefundResult struct {
	Reference   string
	State       string
	AmountMinor int64
}

// Event is a normalized inbound provider notification. A driver maps its own
// event vocabulary onto these kinds so billing never branches on a
// provider-specific string.
type Event struct {
	ID          string
	Kind        string
	Reference   string
	AmountMinor int64
	Currency    string
	Detail      string
	OccurredAt  int64
}

// Normalized webhook event kinds.
const (
	EventPaymentSucceeded = "payment.succeeded"
	EventPaymentFailed    = "payment.failed"
	EventRefunded         = "payment.refunded"
	EventDisputed         = "payment.disputed"
	EventMethodExpiring   = "method.expiring"
	EventIgnored          = "ignored"
)

// Provider is the contract every payment driver implements. A driver that
// cannot perform an operation returns ErrUnsupported rather than pretending
// to succeed.
type Provider interface {
	// Name is the registry key, lower case and stable.
	Name() string
	// DisplayName is what an administrator sees.
	DisplayName() string
	// Category groups the driver in the admin interface.
	Category() string
	// Capabilities declares what this driver can do.
	Capabilities() Capabilities
	// ValidateCredentials checks the configured credentials against the
	// provider, without moving any money.
	ValidateCredentials(ctx context.Context) error
	// StoreMethod converts a client-side token into a stored instrument.
	StoreMethod(ctx context.Context, req MethodRequest) (Method, error)
	// DeleteMethod detaches a stored instrument.
	DeleteMethod(ctx context.Context, token string) error
	// Charge attempts a payment. It must be idempotent on IdempotencyKey.
	Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error)
	// Capture settles a previously authorized charge.
	Capture(ctx context.Context, reference string, amountMinor int64) (ChargeResult, error)
	// Void releases an authorization that was never captured.
	Void(ctx context.Context, reference string) error
	// Refund returns money to the payer.
	Refund(ctx context.Context, req RefundRequest) (RefundResult, error)
	// GetPayment reads the current state of a charge, used by reconciliation.
	GetPayment(ctx context.Context, reference string) (ChargeResult, error)
	// ListPayments returns the provider's own record of charges in a window,
	// which reconciliation compares against cashp's ledger.
	ListPayments(ctx context.Context, since, until int64) ([]ChargeResult, error)
	// VerifyWebhook authenticates a raw inbound delivery and returns the
	// normalized event. It must fail closed on a bad or missing signature.
	VerifyWebhook(ctx context.Context, header http.Header, body []byte, secret string) (Event, error)
}

// Driver is a registered, constructible payment provider.
type Driver struct {
	Name        string
	DisplayName string
	Category    string
	Fields      []Field
	// SetupGuide is the step-by-step text the admin interface shows above the
	// credential form, so an operator can configure the provider without
	// leaving cashp.
	SetupGuide []string
	// Troubleshooting maps a common failure to its remedy, rendered under the
	// test-connection panel.
	Troubleshooting map[string]string
	// New builds a configured instance.
	New func(cfg Config) (Provider, error)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Driver{}
)

// Register adds a driver to the registry. Registration only makes a driver
// configurable: it does not enable it, and no registered driver runs until an
// administrator turns it on.
func Register(d Driver) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if d.Name == "" || d.New == nil {
		return
	}
	registry[d.Name] = d
}

// Lookup returns a registered driver by name.
func Lookup(name string) (Driver, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	d, ok := registry[name]
	if !ok {
		return Driver{}, ErrNotRegistered
	}
	return d, nil
}

// Drivers returns every registered driver, ordered by name.
func Drivers() []Driver {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Driver, 0, len(names))
	for _, name := range names {
		out = append(out, registry[name])
	}
	return out
}

// Names returns the registered driver names, ordered.
func Names() []string {
	drivers := Drivers()
	out := make([]string, 0, len(drivers))
	for _, d := range drivers {
		out = append(out, d.Name)
	}
	return out
}

// RequiredFields returns the names of a driver's mandatory credentials.
func (d Driver) RequiredFields() []string {
	out := []string{}
	for _, f := range d.Fields {
		if f.Required {
			out = append(out, f.Name)
		}
	}
	return out
}

// Validate checks a candidate credential set against a driver's field list
// before anything is encrypted or stored.
func (d Driver) Validate(credentials map[string]string) error {
	for _, f := range d.Fields {
		if !f.Required {
			continue
		}
		if credentials[f.Name] == "" {
			return errors.New("provider: " + f.Label + " is required")
		}
	}
	return nil
}
