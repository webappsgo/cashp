package billing

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"

	"github.com/webappsgo/cashp/src/database"
)

// Tax identifier verification states.
const (
	TaxIDNone       = "none"
	TaxIDUnverified = "unverified"
	TaxIDValid      = "valid"
	TaxIDInvalid    = "invalid"
)

// Tax kinds.
const (
	TaxNone  = "none"
	TaxVAT   = "vat"
	TaxGST   = "gst"
	TaxSales = "sales"
)

// euVATPattern matches the general shape of an EU VAT number: a two-letter
// country prefix followed by eight to twelve alphanumerics. cashp checks the
// format only; it never calls an external verification service on its own,
// because a tax lookup must not be able to block an invoice from being
// raised.
var euVATPattern = regexp.MustCompile(`^[A-Z]{2}[0-9A-Z]{8,12}$`)

// genericTaxIDPattern matches the loose shape of a non-EU business number.
var genericTaxIDPattern = regexp.MustCompile(`^[0-9A-Z][0-9A-Z\-]{3,19}$`)

// euCountries are the member states where the reverse-charge rule applies to
// a validly identified business customer in another member state.
var euCountries = map[string]bool{
	"AT": true, "BE": true, "BG": true, "CY": true, "CZ": true, "DE": true,
	"DK": true, "EE": true, "ES": true, "FI": true, "FR": true, "GR": true,
	"HR": true, "HU": true, "IE": true, "IT": true, "LT": true, "LU": true,
	"LV": true, "MT": true, "NL": true, "PL": true, "PT": true, "RO": true,
	"SE": true, "SI": true, "SK": true,
}

// IsEUCountry reports whether a two-letter country code is an EU member
// state for VAT purposes.
func IsEUCountry(code string) bool {
	return euCountries[strings.ToUpper(strings.TrimSpace(code))]
}

// ValidTaxIDFormat reports whether a tax identifier has a plausible shape for
// a country. An empty identifier is always acceptable: consumers do not have
// one and must never be blocked from paying.
func ValidTaxIDFormat(country, taxID string) bool {
	id := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(taxID), " ", ""))
	if id == "" {
		return true
	}
	if IsEUCountry(country) {
		return euVATPattern.MatchString(id)
	}
	return genericTaxIDPattern.MatchString(id)
}

// TaxResult is the outcome of a tax calculation for one invoice.
type TaxResult struct {
	Kind          string `json:"kind"`
	Jurisdiction  string `json:"jurisdiction"`
	RateBPS       int64  `json:"rate_bps"`
	AmountMinor   int64  `json:"amount_minor"`
	ReverseCharge bool   `json:"reverse_charge"`
	Note          string `json:"note"`
}

// CalculateTax works out the tax due on a net amount for one billing
// account. It never returns an error that would stop an invoice: when no
// rate is on file, or the tax engine is switched off, the answer is simply
// no tax, and the invoice is still raised.
func (s *Service) CalculateTax(ctx context.Context, account Account, netMinor int64) TaxResult {
	if !s.SettingBool(ctx, SettingTaxEnabled, false) || netMinor <= 0 {
		return TaxResult{Kind: TaxNone}
	}
	country := strings.ToUpper(strings.TrimSpace(account.Country))
	if country == "" {
		return TaxResult{Kind: TaxNone, Note: "No customer country on file."}
	}
	rate, err := s.LookupTaxRate(ctx, country, account.Region)
	if err != nil {
		// A missing rate is the ordinary case for a jurisdiction the operator
		// has not registered in; it is not a failure.
		return TaxResult{Kind: TaxNone, Jurisdiction: country}
	}

	sellerCountry := strings.ToUpper(strings.TrimSpace(s.Setting(ctx, "seller_country", "")))
	businessBuyer := account.IsBusiness && account.TaxID != "" && account.TaxIDStatus != TaxIDInvalid
	crossBorderEU := rate.ReverseChargeB2B && businessBuyer &&
		IsEUCountry(country) && IsEUCountry(sellerCountry) && country != sellerCountry
	if crossBorderEU {
		return TaxResult{
			Kind:          rate.Kind,
			Jurisdiction:  jurisdictionOf(rate),
			RateBPS:       0,
			AmountMinor:   0,
			ReverseCharge: true,
			Note:          "Reverse charge: VAT is accounted for by the recipient.",
		}
	}
	return TaxResult{
		Kind:         rate.Kind,
		Jurisdiction: jurisdictionOf(rate),
		RateBPS:      rate.RateBPS,
		AmountMinor:  PercentBasisPoints(netMinor, rate.RateBPS),
		Note:         rate.Name,
	}
}

// jurisdictionOf renders a rate's jurisdiction as "US-CA" or "DE".
func jurisdictionOf(rate TaxRate) string {
	if rate.Region != "" {
		return rate.Country + "-" + rate.Region
	}
	return rate.Country
}

// taxRateColumns is the explicit column list for billing_tax_rates.
const taxRateColumns = `id, country, region, kind, name, rate_bps,
	reverse_charge_b2b, active, updated_at`

// scanTaxRate reads one billing_tax_rates row in taxRateColumns order.
func scanTaxRate(sc interface{ Scan(...any) error }) (TaxRate, error) {
	var t TaxRate
	var reverse, active int64
	if err := sc.Scan(&t.ID, &t.Country, &t.Region, &t.Kind, &t.Name, &t.RateBPS,
		&reverse, &active, &t.UpdatedAt); err != nil {
		return TaxRate{}, err
	}
	t.ReverseChargeB2B = reverse != 0
	t.Active = active != 0
	return t, nil
}

// LookupTaxRate finds the most specific active rate for a location,
// preferring an exact region match over the country-wide rate.
func (s *Service) LookupTaxRate(ctx context.Context, country, region string) (TaxRate, error) {
	country = strings.ToUpper(strings.TrimSpace(country))
	region = strings.ToUpper(strings.TrimSpace(region))
	if region != "" {
		row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
			`SELECT `+taxRateColumns+` FROM billing_tax_rates
			 WHERE country = ? AND region = ? AND active = 1`, country, region)
		rate, err := scanTaxRate(row)
		if err == nil {
			return rate, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return TaxRate{}, ErrInternal(err, "Could not read the tax rate table.")
		}
	}
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+taxRateColumns+` FROM billing_tax_rates
		 WHERE country = ? AND region = '' AND active = 1`, country)
	rate, err := scanTaxRate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TaxRate{}, ErrNotFound("tax rate")
	}
	if err != nil {
		return TaxRate{}, ErrInternal(err, "Could not read the tax rate table.")
	}
	return rate, nil
}

// ListTaxRates returns every rate the operator has entered.
func (s *Service) ListTaxRates(ctx context.Context) ([]TaxRate, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+taxRateColumns+` FROM billing_tax_rates ORDER BY country, region`)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the tax rate table.")
	}
	defer func() { _ = rows.Close() }()

	out := []TaxRate{}
	for rows.Next() {
		rate, sErr := scanTaxRate(rows)
		if sErr != nil {
			return nil, ErrInternal(sErr, "Could not read the tax rate table.")
		}
		out = append(out, rate)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the tax rate table.")
	}
	return out, nil
}

// SaveTaxRate inserts or replaces one jurisdiction rate.
func (s *Service) SaveTaxRate(ctx context.Context, rate TaxRate, actor, ip string) (TaxRate, error) {
	rate.Country = strings.ToUpper(strings.TrimSpace(rate.Country))
	rate.Region = strings.ToUpper(strings.TrimSpace(rate.Region))
	if len(rate.Country) != 2 {
		return TaxRate{}, ErrValidation("The country must be a two-letter ISO 3166-1 code.")
	}
	if rate.RateBPS < 0 || rate.RateBPS > 10000 {
		return TaxRate{}, ErrValidation("The rate must be between 0 and 10000 basis points.")
	}
	switch rate.Kind {
	case TaxVAT, TaxGST, TaxSales:
	default:
		return TaxRate{}, ErrValidation("The tax kind must be vat, gst or sales.")
	}

	now := s.unix()
	existing, err := s.taxRateFor(ctx, rate.Country, rate.Region)
	switch {
	case err == nil:
		rate.ID = existing.ID
		_, uErr := s.db.ExecContext(ctx, database.TimeoutWrite,
			`UPDATE billing_tax_rates SET kind = ?, name = ?, rate_bps = ?,
			   reverse_charge_b2b = ?, active = ?, updated_at = ?
			 WHERE id = ?`,
			rate.Kind, rate.Name, rate.RateBPS, boolToInt(rate.ReverseChargeB2B),
			boolToInt(rate.Active), now, rate.ID)
		if uErr != nil {
			return TaxRate{}, ErrInternal(uErr, "Could not save the tax rate.")
		}
	case isNotFound(err):
		rate.ID = newID()
		_, iErr := s.db.ExecContext(ctx, database.TimeoutWrite,
			`INSERT INTO billing_tax_rates
			 (id, country, region, kind, name, rate_bps, reverse_charge_b2b, active, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			rate.ID, rate.Country, rate.Region, rate.Kind, rate.Name, rate.RateBPS,
			boolToInt(rate.ReverseChargeB2B), boolToInt(rate.Active), now)
		if iErr != nil {
			return TaxRate{}, ErrInternal(iErr, "Could not save the tax rate.")
		}
	default:
		return TaxRate{}, err
	}
	rate.UpdatedAt = now

	s.WriteAudit(ctx, AuditRecord{
		Actor: actor, Action: ActionSettingChanged,
		Target: "tax_rate:" + jurisdictionOf(rate), IP: ip,
		Detail: "rate_bps=" + itoa(rate.RateBPS),
	})
	return rate, nil
}

// taxRateFor reads the rate row for an exact country and region pair.
func (s *Service) taxRateFor(ctx context.Context, country, region string) (TaxRate, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+taxRateColumns+` FROM billing_tax_rates
		 WHERE country = ? AND region = ?`, country, region)
	rate, err := scanTaxRate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TaxRate{}, ErrNotFound("tax rate")
	}
	if err != nil {
		return TaxRate{}, ErrInternal(err, "Could not read the tax rate table.")
	}
	return rate, nil
}
