package billing

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/webappsgo/cashp/src/database"
)

// accountColumns is the explicit column list for billing_accounts. Column
// lists in this package are always written out: no query anywhere uses
// SELECT *, so adding a column can never silently change a scan.
const accountColumns = `id, tenant_id, currency, billing_email, legal_name,
	address_line1, address_line2, city, region, postal_code, country,
	tax_id, tax_id_status, is_business, balance_minor, auto_recharge,
	auto_recharge_threshold_minor, auto_recharge_amount_minor,
	default_method_id, created_at, updated_at, version`

// scanAccount reads one billing_accounts row in accountColumns order.
func scanAccount(sc interface{ Scan(...any) error }) (Account, error) {
	var a Account
	var isBusiness, autoRecharge int64
	err := sc.Scan(&a.ID, &a.TenantID, &a.Currency, &a.BillingEmail, &a.LegalName,
		&a.AddressLine1, &a.AddressLine2, &a.City, &a.Region, &a.PostalCode, &a.Country,
		&a.TaxID, &a.TaxIDStatus, &isBusiness, &a.BalanceMinor, &autoRecharge,
		&a.AutoRechargeThreshold, &a.AutoRechargeAmountMinor,
		&a.DefaultMethodID, &a.CreatedAt, &a.UpdatedAt, &a.Version)
	if err != nil {
		return Account{}, err
	}
	a.IsBusiness = isBusiness != 0
	a.AutoRecharge = autoRecharge != 0
	return a, nil
}

// Account returns a tenant's billing profile. The tenant is always part of
// the predicate, so a caller holding only an account id can never read
// another tenant's profile through this method.
func (s *Service) Account(ctx context.Context, tenantID string) (Account, error) {
	if strings.TrimSpace(tenantID) == "" {
		return Account{}, ErrValidation("billing: a tenant is required")
	}
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+accountColumns+` FROM billing_accounts WHERE tenant_id = ?`, tenantID)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound("billing account")
	}
	if err != nil {
		return Account{}, ErrInternal(err, "Could not read the billing account.")
	}
	return a, nil
}

// EnsureAccount returns a tenant's billing profile, creating an empty one in
// the operator's base currency the first time it is needed.
func (s *Service) EnsureAccount(ctx context.Context, tenantID string) (Account, error) {
	a, err := s.Account(ctx, tenantID)
	if err == nil {
		return a, nil
	}
	if !isNotFound(err) {
		return Account{}, err
	}
	now := s.unix()
	a = Account{
		ID:        newID(),
		TenantID:  tenantID,
		Currency:  s.BaseCurrency(ctx),
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}
	_, err = s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_accounts
		 (id, tenant_id, currency, created_at, updated_at, version)
		 VALUES (?, ?, ?, ?, ?, 1)`,
		a.ID, a.TenantID, a.Currency, now, now)
	if err != nil {
		// A concurrent request may have created the row between the read and
		// this insert; re-read rather than reporting a conflict upwards.
		if database.IsConflict(err) || database.IsAlreadyExistsError(err) {
			return s.Account(ctx, tenantID)
		}
		return Account{}, ErrInternal(err, "Could not create the billing account.")
	}
	return a, nil
}

// AccountUpdate carries the fields a tenant may change on their own profile.
type AccountUpdate struct {
	Currency                string
	BillingEmail            string
	LegalName               string
	AddressLine1            string
	AddressLine2            string
	City                    string
	Region                  string
	PostalCode              string
	Country                 string
	TaxID                   string
	IsBusiness              bool
	AutoRecharge            bool
	AutoRechargeThreshold   int64
	AutoRechargeAmountMinor int64
}

// UpdateAccount saves a tenant's billing profile. The currency may only
// change while the tenant has no issued invoices, because an invoice's
// figures are meaningless in a currency other than the one it was raised in.
func (s *Service) UpdateAccount(ctx context.Context, tenantID string, in AccountUpdate, actor, ip string) (Account, error) {
	current, err := s.EnsureAccount(ctx, tenantID)
	if err != nil {
		return Account{}, err
	}
	currency := current.Currency
	if in.Currency != "" {
		next, cErr := NormalizeCurrency(in.Currency)
		if cErr != nil {
			return Account{}, ErrValidation(cErr.Error())
		}
		if next != current.Currency {
			issued, cntErr := s.countIssuedInvoices(ctx, tenantID)
			if cntErr != nil {
				return Account{}, cntErr
			}
			if issued > 0 {
				return Account{}, ErrConflict("The billing currency cannot change once invoices have been issued.")
			}
			currency = next
		}
	}
	country := strings.ToUpper(strings.TrimSpace(in.Country))
	if country != "" && len(country) != 2 {
		return Account{}, ErrValidation("The country must be a two-letter ISO 3166-1 code.")
	}
	if in.AutoRecharge && in.AutoRechargeAmountMinor <= 0 {
		return Account{}, ErrValidation("Automatic top-up needs an amount greater than zero.")
	}

	taxID := strings.ToUpper(strings.TrimSpace(in.TaxID))
	taxStatus := current.TaxIDStatus
	if taxID != current.TaxID {
		taxStatus = TaxIDUnverified
		if taxID == "" {
			taxStatus = TaxIDNone
		} else if !ValidTaxIDFormat(country, taxID) {
			return Account{}, ErrValidation("That tax identifier is not in the expected format for " + country + ".")
		}
	}

	now := s.unix()
	err = s.db.UpdateVersioned(ctx,
		`UPDATE billing_accounts SET
		   currency = ?, billing_email = ?, legal_name = ?,
		   address_line1 = ?, address_line2 = ?, city = ?, region = ?,
		   postal_code = ?, country = ?, tax_id = ?, tax_id_status = ?,
		   is_business = ?, auto_recharge = ?,
		   auto_recharge_threshold_minor = ?, auto_recharge_amount_minor = ?,
		   updated_at = ?, version = version + 1
		 WHERE tenant_id = ? AND version = ?`,
		currency, strings.TrimSpace(in.BillingEmail), strings.TrimSpace(in.LegalName),
		strings.TrimSpace(in.AddressLine1), strings.TrimSpace(in.AddressLine2),
		strings.TrimSpace(in.City), strings.TrimSpace(in.Region),
		strings.TrimSpace(in.PostalCode), country, taxID, taxStatus,
		boolToInt(in.IsBusiness), boolToInt(in.AutoRecharge),
		ClampNonNegative(in.AutoRechargeThreshold), ClampNonNegative(in.AutoRechargeAmountMinor),
		now, tenantID, current.Version)
	if err != nil {
		if database.IsConflict(err) {
			return Account{}, ErrConflict("The billing profile changed while you were editing it; reload and try again.")
		}
		return Account{}, ErrInternal(err, "Could not save the billing profile.")
	}

	s.WriteAudit(ctx, AuditRecord{
		TenantID: tenantID, Actor: actor, Action: ActionAccountUpdated,
		Target: "account:" + current.ID, IP: ip, Result: ResultSuccess,
	})
	return s.Account(ctx, tenantID)
}

// SetDefaultMethod points a tenant's account at one of its own payment
// methods. The method is re-read under the tenant predicate first, so an
// identifier belonging to another tenant is rejected as not found.
func (s *Service) SetDefaultMethod(ctx context.Context, tenantID, methodID string) error {
	method, err := s.PaymentMethod(ctx, tenantID, methodID)
	if err != nil {
		return err
	}
	now := s.unix()
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_payment_methods SET is_default = 0, updated_at = ?
		 WHERE tenant_id = ?`, now, tenantID); err != nil {
		return ErrInternal(err, "Could not change the default payment method.")
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_payment_methods SET is_default = 1, updated_at = ?
		 WHERE tenant_id = ? AND id = ?`, now, tenantID, method.ID); err != nil {
		return ErrInternal(err, "Could not change the default payment method.")
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_accounts SET default_method_id = ?, updated_at = ?
		 WHERE tenant_id = ?`, method.ID, now, tenantID); err != nil {
		return ErrInternal(err, "Could not change the default payment method.")
	}
	return nil
}

// countIssuedInvoices counts a tenant's invoices past draft.
func (s *Service) countIssuedInvoices(ctx context.Context, tenantID string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT COUNT(*) FROM billing_invoices WHERE tenant_id = ? AND state <> ?`,
		tenantID, InvoiceDraft).Scan(&n)
	if err != nil {
		return 0, ErrInternal(err, "Could not read the tenant's invoices.")
	}
	return n, nil
}
