// Package stripe implements the cashp payment provider contract against
// Stripe's REST API over net/http. It deliberately does not use Stripe's Go
// SDK: cashp stays a single static binary with a stdlib-only dependency
// surface, and the handful of endpoints billing needs are plain form posts.
//
// The driver is registered but disabled. Nothing here runs until an
// administrator enters credentials and enables Stripe in the admin interface.
package stripe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/billing/provider"
	"github.com/webappsgo/cashp/src/security"
)

// apiBase is Stripe's REST endpoint. Stripe has no separate sandbox host:
// test mode is selected by the key, so a test key and a live key hit the same
// URL and can never be confused for one another.
const apiBase = "https://api.stripe.com/v1"

// webhookTolerance is how far an inbound signature timestamp may drift before
// the delivery is rejected as a replay.
const webhookTolerance = 5 * time.Minute

// Credential field names.
const (
	fieldSecretKey      = "secret_key"
	fieldPublishableKey = "publishable_key"
	fieldWebhookSecret  = "webhook_secret"
)

// client is a configured Stripe driver.
type client struct {
	secretKey string
	testMode  bool
	http      *http.Client
}

func init() {
	provider.Register(provider.Driver{
		Name:        "stripe",
		DisplayName: "Stripe",
		Category:    provider.CategoryGlobal,
		Fields: []provider.Field{
			{
				Name:        fieldPublishableKey,
				Label:       "Publishable key",
				Required:    true,
				Placeholder: "pk_test_...",
				Tooltip: "Stripe's public key, safe to send to the browser. " +
					"Find it in the Stripe Dashboard under Developers, API keys, " +
					"Publishable key. It begins pk_test_ in test mode and pk_live_ " +
					"in live mode. cashp hands this to the payment form so card " +
					"details go straight to Stripe and never reach this server.",
			},
			{
				Name:        fieldSecretKey,
				Label:       "Secret key",
				Required:    true,
				Secret:      true,
				Placeholder: "sk_test_...",
				Tooltip: "Stripe's private key, used server to server only. Find " +
					"it in the Stripe Dashboard under Developers, API keys, Secret " +
					"key, and click Reveal. It begins sk_test_ in test mode and " +
					"sk_live_ in live mode. Treat it like a password: cashp stores " +
					"it encrypted, never logs it and never shows it again after " +
					"saving. If it is ever exposed, roll it in the Stripe Dashboard.",
			},
			{
				Name:        fieldWebhookSecret,
				Label:       "Webhook signing secret",
				Required:    true,
				Secret:      true,
				Placeholder: "whsec_...",
				Tooltip: "The signing secret for the webhook endpoint you added in " +
					"Stripe. Find it in the Stripe Dashboard under Developers, " +
					"Webhooks, by selecting your cashp endpoint and clicking Reveal " +
					"under Signing secret. It begins whsec_. cashp verifies every " +
					"inbound delivery against this value and rejects anything that " +
					"does not match, so a forged webhook cannot mark an invoice paid.",
			},
		},
		SetupGuide: []string{
			"Create or sign in to a Stripe account at dashboard.stripe.com.",
			"Leave the dashboard's test-mode switch on while you set cashp up; use test keys until you have completed a test charge.",
			"Open Developers, API keys and copy the publishable key and the secret key into the fields below.",
			"Open Developers, Webhooks and add an endpoint pointing at the webhook URL shown on this page.",
			"Subscribe that endpoint to payment_intent.succeeded, payment_intent.payment_failed, charge.refunded and charge.dispute.created.",
			"Copy the endpoint's signing secret into the webhook signing secret field below.",
			"Save, then run Test connection. Only after it passes should you switch this provider out of test mode.",
		},
		Troubleshooting: map[string]string{
			"Invalid API key provided":   "The secret key is wrong, was rolled in the Stripe Dashboard, or is a live key entered while cashp is in test mode. Copy it again from Developers, API keys.",
			"No such payment_method":     "The stored card belongs to a different Stripe account, usually because keys were switched between test and live. Ask the tenant to add the payment method again.",
			"Webhook signature mismatch": "The signing secret does not match this endpoint. Each endpoint has its own secret; copy the one shown for the cashp endpoint specifically.",
			"Your card was declined":     "The issuer refused the charge. This is not a configuration problem: cashp records the failure and dunning retries on the schedule you configured.",
			"Timeout":                    "cashp could not reach api.stripe.com within the provider timeout. Check outbound network access and any egress firewall on this host.",
		},
		New: newClient,
	})
}

// newClient builds a configured Stripe driver.
func newClient(cfg provider.Config) (provider.Provider, error) {
	key := strings.TrimSpace(cfg.Credentials[fieldSecretKey])
	if key == "" {
		return nil, errors.New("stripe: a secret key is required")
	}
	live := strings.HasPrefix(key, "sk_live_")
	if cfg.TestMode && live {
		return nil, errors.New("stripe: a live secret key cannot be used while the provider is in test mode")
	}
	if !cfg.TestMode && !live {
		return nil, errors.New("stripe: a test secret key cannot be used once the provider is live")
	}
	return &client{secretKey: key, testMode: cfg.TestMode, http: cfg.Client()}, nil
}

// Name returns the registry key.
func (c *client) Name() string { return "stripe" }

// DisplayName returns the administrator-facing name.
func (c *client) DisplayName() string { return "Stripe" }

// Category returns the driver's grouping.
func (c *client) Category() string { return provider.CategoryGlobal }

// Capabilities declares what this driver supports. cashp keeps subscription
// state itself rather than mirroring it into Stripe, so provider-side
// subscriptions are deliberately not claimed.
func (c *client) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Charge:               true,
		Authorize:            true,
		Refund:               true,
		StoreMethod:          true,
		ProviderSubscription: false,
		Webhooks:             true,
	}
}

// stripeError is Stripe's error envelope.
type stripeError struct {
	Error struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
		Param   string `json:"param"`
	} `json:"error"`
}

// do performs one Stripe API call. Every request is checked by
// security.ValidateOutboundURL first, so a malformed or redirected endpoint
// can never be used to reach an internal address.
func (c *client) do(ctx context.Context, method, path string, form url.Values, idempotencyKey string, out any) error {
	endpoint := apiBase + path
	if method == http.MethodGet && len(form) > 0 {
		endpoint += "?" + form.Encode()
	}
	if err := security.ValidateOutboundURL(endpoint); err != nil {
		return fmt.Errorf("stripe: %w", err)
	}

	var body io.Reader
	if method != http.MethodGet && len(form) > 0 {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("stripe: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.secretKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("stripe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("stripe: %w", err)
	}
	if resp.StatusCode >= 400 {
		var se stripeError
		if json.Unmarshal(payload, &se) == nil && se.Error.Message != "" {
			return &APIError{Code: se.Error.Code, Type: se.Error.Type, Message: se.Error.Message}
		}
		return &APIError{Code: strconv.Itoa(resp.StatusCode), Message: "Stripe returned an unexpected response."}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("stripe: %w", err)
	}
	return nil
}

// APIError is a structured Stripe failure, carrying the decline code billing
// records on the payment attempt.
type APIError struct {
	Code    string
	Type    string
	Message string
}

// Error renders the failure.
func (e *APIError) Error() string {
	if e.Code == "" {
		return "stripe: " + e.Message
	}
	return "stripe: " + e.Code + ": " + e.Message
}

// ValidateCredentials confirms the secret key works without moving money.
func (c *client) ValidateCredentials(ctx context.Context) error {
	var out struct {
		Object string `json:"object"`
	}
	if err := c.do(ctx, http.MethodGet, "/balance", nil, "", &out); err != nil {
		return err
	}
	if out.Object != "balance" {
		return errors.New("stripe: unexpected response while validating credentials")
	}
	return nil
}

// cardDetail is the display-only card information Stripe returns. These are
// the only card-derived values cashp ever stores.
type cardDetail struct {
	Brand    string `json:"brand"`
	Last4    string `json:"last4"`
	ExpMonth int64  `json:"exp_month"`
	ExpYear  int64  `json:"exp_year"`
	Country  string `json:"country"`
}

// paymentMethod is the subset of Stripe's payment method object cashp keeps.
type paymentMethod struct {
	ID   string     `json:"id"`
	Type string     `json:"type"`
	Card cardDetail `json:"card"`
}

// StoreMethod attaches a browser-produced token to a Stripe customer. The
// token is the only card-derived value that reaches cashp; the card number
// and CVV go from the tenant's browser straight to Stripe.
func (c *client) StoreMethod(ctx context.Context, req provider.MethodRequest) (provider.Method, error) {
	customer := req.CustomerRef
	if customer == "" {
		form := url.Values{}
		if req.BillingEmail != "" {
			form.Set("email", req.BillingEmail)
		}
		if req.HolderName != "" {
			form.Set("name", req.HolderName)
		}
		form.Set("metadata[tenant_id]", req.TenantID)
		var created struct {
			ID string `json:"id"`
		}
		if err := c.do(ctx, http.MethodPost, "/customers", form, "", &created); err != nil {
			return provider.Method{}, err
		}
		customer = created.ID
	}

	form := url.Values{}
	form.Set("customer", customer)
	var pm paymentMethod
	path := "/payment_methods/" + url.PathEscape(req.Token) + "/attach"
	if err := c.do(ctx, http.MethodPost, path, form, "", &pm); err != nil {
		return provider.Method{}, err
	}
	return provider.Method{
		Token:       pm.ID,
		CustomerRef: customer,
		Kind:        pm.Type,
		Brand:       pm.Card.Brand,
		Last4:       pm.Card.Last4,
		ExpMonth:    pm.Card.ExpMonth,
		ExpYear:     pm.Card.ExpYear,
		Country:     pm.Card.Country,
	}, nil
}

// DeleteMethod detaches a stored instrument from its customer.
func (c *client) DeleteMethod(ctx context.Context, token string) error {
	path := "/payment_methods/" + url.PathEscape(token) + "/detach"
	return c.do(ctx, http.MethodPost, path, url.Values{}, "", nil)
}

// redirectAction is the redirect Stripe asks the payer to follow when a
// charge needs their bank's confirmation.
type redirectAction struct {
	URL string `json:"url"`
}

// nextAction is Stripe's follow-up instruction for a charge.
type nextAction struct {
	RedirectToURL *redirectAction `json:"redirect_to_url"`
}

// paymentError is Stripe's decline detail.
type paymentError struct {
	Code        string `json:"code"`
	DeclineCode string `json:"decline_code"`
	Message     string `json:"message"`
}

// paymentIntent is the subset of Stripe's payment intent cashp reads.
type paymentIntent struct {
	ID               string        `json:"id"`
	Status           string        `json:"status"`
	Amount           int64         `json:"amount"`
	Currency         string        `json:"currency"`
	Created          int64         `json:"created"`
	NextAction       *nextAction   `json:"next_action"`
	LastPaymentError *paymentError `json:"last_payment_error"`
}

// result converts a Stripe payment intent into the normalized outcome.
func (p paymentIntent) result() provider.ChargeResult {
	res := provider.ChargeResult{
		Reference:   p.ID,
		AmountMinor: p.Amount,
		Currency:    strings.ToUpper(p.Currency),
	}
	switch p.Status {
	case "succeeded":
		res.State = provider.StateSucceeded
	case "requires_capture":
		res.State = provider.StateAuthorized
	case "processing", "requires_confirmation":
		res.State = provider.StatePending
	case "requires_action":
		res.State = provider.StatePending
		res.RequiresAction = true
		if p.NextAction != nil && p.NextAction.RedirectToURL != nil {
			res.ActionURL = p.NextAction.RedirectToURL.URL
		}
	default:
		res.State = provider.StateFailed
	}
	if p.LastPaymentError != nil {
		res.FailureCode = p.LastPaymentError.DeclineCode
		if res.FailureCode == "" {
			res.FailureCode = p.LastPaymentError.Code
		}
		res.FailureMessage = p.LastPaymentError.Message
		if res.State != provider.StateSucceeded {
			res.State = provider.StateFailed
		}
	}
	return res
}

// Charge attempts an off-session payment. The idempotency key is passed to
// Stripe, so a retried request returns the original charge instead of taking
// the money a second time.
func (c *client) Charge(ctx context.Context, req provider.ChargeRequest) (provider.ChargeResult, error) {
	form := url.Values{}
	form.Set("amount", strconv.FormatInt(req.AmountMinor, 10))
	form.Set("currency", strings.ToLower(req.Currency))
	form.Set("payment_method", req.MethodToken)
	form.Set("confirm", "true")
	form.Set("off_session", "true")
	form.Set("capture_method", "automatic")
	if !req.Capture {
		form.Set("capture_method", "manual")
	}
	if req.CustomerRef != "" {
		form.Set("customer", req.CustomerRef)
	}
	if req.Description != "" {
		form.Set("description", req.Description)
	}
	if req.InvoiceNumber != "" {
		form.Set("metadata[invoice_number]", req.InvoiceNumber)
	}
	// Stamping the mode makes a sandbox charge obvious in Stripe's own
	// dashboard as well as in cashp's reconciliation report.
	if c.testMode {
		form.Set("metadata[cashp_mode]", "test")
	} else {
		form.Set("metadata[cashp_mode]", "live")
	}

	var pi paymentIntent
	if err := c.do(ctx, http.MethodPost, "/payment_intents", form, req.IdempotencyKey, &pi); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			return provider.ChargeResult{
				State:          provider.StateFailed,
				AmountMinor:    req.AmountMinor,
				Currency:       req.Currency,
				FailureCode:    apiErr.Code,
				FailureMessage: apiErr.Message,
			}, nil
		}
		return provider.ChargeResult{}, err
	}
	return pi.result(), nil
}

// Capture settles a previously authorized charge.
func (c *client) Capture(ctx context.Context, reference string, amountMinor int64) (provider.ChargeResult, error) {
	form := url.Values{}
	if amountMinor > 0 {
		form.Set("amount_to_capture", strconv.FormatInt(amountMinor, 10))
	}
	var pi paymentIntent
	path := "/payment_intents/" + url.PathEscape(reference) + "/capture"
	if err := c.do(ctx, http.MethodPost, path, form, "", &pi); err != nil {
		return provider.ChargeResult{}, err
	}
	return pi.result(), nil
}

// Void releases an authorization that was never captured.
func (c *client) Void(ctx context.Context, reference string) error {
	path := "/payment_intents/" + url.PathEscape(reference) + "/cancel"
	return c.do(ctx, http.MethodPost, path, url.Values{}, "", nil)
}

// Refund returns money to the payer.
func (c *client) Refund(ctx context.Context, req provider.RefundRequest) (provider.RefundResult, error) {
	form := url.Values{}
	form.Set("payment_intent", req.Reference)
	if req.AmountMinor > 0 {
		form.Set("amount", strconv.FormatInt(req.AmountMinor, 10))
	}
	if req.Reason != "" {
		form.Set("metadata[reason]", req.Reason)
	}
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Amount int64  `json:"amount"`
	}
	if err := c.do(ctx, http.MethodPost, "/refunds", form, req.IdempotencyKey, &out); err != nil {
		return provider.RefundResult{}, err
	}
	state := provider.StatePending
	if out.Status == "succeeded" {
		state = provider.StateSucceeded
	}
	if out.Status == "failed" || out.Status == "canceled" {
		state = provider.StateFailed
	}
	return provider.RefundResult{Reference: out.ID, State: state, AmountMinor: out.Amount}, nil
}

// GetPayment reads one charge's current state.
func (c *client) GetPayment(ctx context.Context, reference string) (provider.ChargeResult, error) {
	var pi paymentIntent
	path := "/payment_intents/" + url.PathEscape(reference)
	if err := c.do(ctx, http.MethodGet, path, nil, "", &pi); err != nil {
		return provider.ChargeResult{}, err
	}
	return pi.result(), nil
}

// ListPayments returns Stripe's own record of charges in a window, which the
// reconciliation task compares against cashp's ledger.
func (c *client) ListPayments(ctx context.Context, since, until int64) ([]provider.ChargeResult, error) {
	form := url.Values{}
	form.Set("limit", "100")
	form.Set("created[gte]", strconv.FormatInt(since, 10))
	form.Set("created[lte]", strconv.FormatInt(until, 10))

	out := []provider.ChargeResult{}
	startingAfter := ""
	for page := 0; page < 20; page++ {
		if startingAfter != "" {
			form.Set("starting_after", startingAfter)
		}
		var listed struct {
			Data    []paymentIntent `json:"data"`
			HasMore bool            `json:"has_more"`
		}
		if err := c.do(ctx, http.MethodGet, "/payment_intents", form, "", &listed); err != nil {
			return nil, err
		}
		for _, pi := range listed.Data {
			out = append(out, pi.result())
			startingAfter = pi.ID
		}
		if !listed.HasMore || len(listed.Data) == 0 {
			break
		}
	}
	return out, nil
}

// webhookObject is the payload object carried by a Stripe event.
type webhookObject struct {
	ID            string `json:"id"`
	PaymentIntent string `json:"payment_intent"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
}

// webhookData wraps the object in Stripe's event envelope.
type webhookData struct {
	Object webhookObject `json:"object"`
}

// webhookEnvelope is the subset of Stripe's event object cashp reads.
type webhookEnvelope struct {
	ID      string      `json:"id"`
	Type    string      `json:"type"`
	Created int64       `json:"created"`
	Data    webhookData `json:"data"`
}

// VerifyWebhook authenticates an inbound Stripe delivery. It fails closed:
// a missing header, an unparsable header, a stale timestamp or a signature
// that does not match all cause a rejection, and the comparison itself is
// constant time so a caller cannot probe for a valid signature byte by byte.
func (c *client) VerifyWebhook(ctx context.Context, header http.Header, body []byte, secret string) (provider.Event, error) {
	if secret == "" {
		return provider.Event{}, errors.New("stripe: no webhook signing secret is configured")
	}
	sigHeader := header.Get("Stripe-Signature")
	if sigHeader == "" {
		return provider.Event{}, errors.New("stripe: the delivery carried no signature")
	}
	timestamp, signatures, err := parseSignatureHeader(sigHeader)
	if err != nil {
		return provider.Event{}, err
	}
	age := time.Now().Unix() - timestamp
	if age < 0 {
		age = -age
	}
	if age > int64(webhookTolerance.Seconds()) {
		return provider.Event{}, errors.New("stripe: the delivery signature is outside the accepted time window")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	matched := false
	for _, candidate := range signatures {
		if security.ConstantTimeEqualString(candidate, expected) {
			matched = true
		}
	}
	if !matched {
		return provider.Event{}, errors.New("stripe: the delivery signature did not match")
	}

	var env webhookEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return provider.Event{}, fmt.Errorf("stripe: %w", err)
	}
	reference := env.Data.Object.PaymentIntent
	if reference == "" {
		reference = env.Data.Object.ID
	}
	return provider.Event{
		ID:          env.ID,
		Kind:        mapEventKind(env.Type),
		Reference:   reference,
		AmountMinor: env.Data.Object.Amount,
		Currency:    strings.ToUpper(env.Data.Object.Currency),
		Detail:      env.Type,
		OccurredAt:  env.Created,
	}, nil
}

// parseSignatureHeader splits Stripe's t=,v1= signature header. Stripe sends
// every currently valid signature, so more than one v1 element is normal
// during a secret roll.
func parseSignatureHeader(header string) (int64, []string, error) {
	var timestamp int64
	signatures := []string{}
	for _, part := range strings.Split(header, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch key {
		case "t":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return 0, nil, errors.New("stripe: the delivery signature timestamp was not a number")
			}
			timestamp = parsed
		case "v1":
			signatures = append(signatures, value)
		}
	}
	if timestamp == 0 || len(signatures) == 0 {
		return 0, nil, errors.New("stripe: the delivery signature header was malformed")
	}
	return timestamp, signatures, nil
}

// mapEventKind translates Stripe's event vocabulary into the normalized kinds
// billing understands, so no Stripe-specific string escapes this package.
func mapEventKind(stripeType string) string {
	switch stripeType {
	case "payment_intent.succeeded", "charge.succeeded":
		return provider.EventPaymentSucceeded
	case "payment_intent.payment_failed", "charge.failed":
		return provider.EventPaymentFailed
	case "charge.refunded", "refund.created":
		return provider.EventRefunded
	case "charge.dispute.created":
		return provider.EventDisputed
	case "payment_method.automatically_updated", "customer.source.expiring":
		return provider.EventMethodExpiring
	default:
		return provider.EventIgnored
	}
}
