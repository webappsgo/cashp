package billing

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

// Usage metering. Counters accumulate over a period, gauges hold the latest
// observation, and histograms keep a running sum with a sample count so an
// average can be derived without storing every sample.

// meterColumns is the explicit column list for billing_usage_meters.
const meterColumns = `code, name, meter_type, resource, unit, reset_policy,
	created_at, updated_at`

// scanMeter reads one billing_usage_meters row in meterColumns order.
func scanMeter(sc interface{ Scan(...any) error }) (Meter, error) {
	var m Meter
	err := sc.Scan(&m.Code, &m.Name, &m.MeterType, &m.Resource, &m.Unit,
		&m.ResetPolicy, &m.CreatedAt, &m.UpdatedAt)
	return m, err
}

// Meter returns one meter definition.
func (s *Service) Meter(ctx context.Context, code string) (Meter, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+meterColumns+` FROM billing_usage_meters WHERE code = ?`,
		strings.ToLower(strings.TrimSpace(code)))
	m, err := scanMeter(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Meter{}, ErrNotFound("meter")
	}
	if err != nil {
		return Meter{}, ErrInternal(err, "Could not read the meter.")
	}
	return m, nil
}

// ListMeters returns every meter definition.
func (s *Service) ListMeters(ctx context.Context) ([]Meter, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+meterColumns+` FROM billing_usage_meters ORDER BY code`)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the meters.")
	}
	defer func() { _ = rows.Close() }()

	out := []Meter{}
	for rows.Next() {
		m, sErr := scanMeter(rows)
		if sErr != nil {
			return nil, ErrInternal(sErr, "Could not read the meters.")
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the meters.")
	}
	return out, nil
}

// SaveMeter inserts or updates one meter definition.
func (s *Service) SaveMeter(ctx context.Context, m Meter) (Meter, error) {
	m.Code = strings.ToLower(strings.TrimSpace(m.Code))
	if m.Code == "" {
		return Meter{}, ErrValidation("A meter needs a code.")
	}
	switch m.MeterType {
	case MeterCounter, MeterGauge, MeterHistogram:
	default:
		return Meter{}, ErrValidation("A meter must be a counter, a gauge or a histogram.")
	}
	switch m.ResetPolicy {
	case ResetHard, ResetRolling, ResetCarry, ResetAccumulate:
	default:
		return Meter{}, ErrValidation("That meter reset policy is not recognised.")
	}
	if m.Resource != "" && !ValidResource(m.Resource) {
		return Meter{}, ErrValidation("Unknown quota resource " + m.Resource + ".")
	}
	if m.Unit == "" && m.Resource != "" {
		m.Unit = ResourceUnit(m.Resource)
	}

	now := s.unix()
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_usage_meters SET name = ?, meter_type = ?, resource = ?,
		   unit = ?, reset_policy = ?, updated_at = ?
		 WHERE code = ?`,
		m.Name, m.MeterType, m.Resource, m.Unit, m.ResetPolicy, now, m.Code)
	if err != nil {
		return Meter{}, ErrInternal(err, "Could not save the meter.")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, iErr := s.db.ExecContext(ctx, database.TimeoutWrite,
			`INSERT INTO billing_usage_meters
			 (code, name, meter_type, resource, unit, reset_policy, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			m.Code, m.Name, m.MeterType, m.Resource, m.Unit, m.ResetPolicy,
			now, now); iErr != nil {
			return Meter{}, ErrInternal(iErr, "Could not save the meter.")
		}
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	return m, nil
}

// EnsureDefaultMeters registers one gauge meter per quota resource so a
// tenant's consumption is measurable the moment billing is switched on. It
// is idempotent and safe to call on every start.
func (s *Service) EnsureDefaultMeters(ctx context.Context) error {
	for _, resource := range Resources() {
		if _, err := s.Meter(ctx, resource); err == nil {
			continue
		} else if !isNotFound(err) {
			return err
		}
		if _, err := s.SaveMeter(ctx, Meter{
			Code:        resource,
			Name:        strings.ReplaceAll(resource, "_", " "),
			MeterType:   MeterGauge,
			Resource:    resource,
			Unit:        ResourceUnit(resource),
			ResetPolicy: ResetHard,
		}); err != nil {
			return err
		}
	}
	return nil
}

// CurrentPeriod returns the billing period a tenant is currently inside.
// A tenant with no subscription is measured against the calendar month so
// usage is still reported before any plan exists.
func (s *Service) CurrentPeriod(ctx context.Context, tenantID string) (int64, int64) {
	sub, err := s.ActiveSubscription(ctx, tenantID)
	if err == nil && sub.PeriodStart > 0 && sub.PeriodEnd > sub.PeriodStart {
		return sub.PeriodStart, sub.PeriodEnd
	}
	now := s.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start.Unix(), start.AddDate(0, 1, 0).Unix()
}

// RecordUsage stores one measurement. A counter adds to the period's running
// total, a gauge replaces it, and a histogram adds to the sum while counting
// the sample.
func (s *Service) RecordUsage(ctx context.Context, tenantID, meterCode string, value int64, dimensions string) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrValidation("billing: a tenant is required")
	}
	meter, err := s.Meter(ctx, meterCode)
	if err != nil {
		return err
	}
	start, end := s.CurrentPeriod(ctx, tenantID)
	now := s.unix()

	existing, err := s.usageRecord(ctx, tenantID, meter.Code, start, dimensions)
	switch {
	case isNotFound(err):
		_, iErr := s.db.ExecContext(ctx, database.TimeoutWrite,
			`INSERT INTO billing_usage_records
			 (id, tenant_id, meter_code, period_start, period_end, value, samples,
			  state, dimensions, recorded_at)
			 VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?)`,
			newID(), tenantID, meter.Code, start, end, value, UsageIncluded,
			dimensions, now)
		if iErr != nil {
			return ErrInternal(iErr, "Could not record the usage measurement.")
		}
		return nil
	case err != nil:
		return err
	}

	next := existing.Value + value
	if meter.MeterType == MeterGauge {
		next = value
	}
	if _, uErr := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_usage_records SET value = ?, samples = samples + 1,
		   period_end = ?, recorded_at = ?
		 WHERE id = ?`, next, end, now, existing.ID); uErr != nil {
		return ErrInternal(uErr, "Could not record the usage measurement.")
	}
	return nil
}

// usageRecord reads one tenant's record for a meter, period and dimension
// set.
func (s *Service) usageRecord(ctx context.Context, tenantID, meterCode string, start int64, dimensions string) (UsageRecord, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT id, tenant_id, meter_code, period_start, period_end, value,
		        samples, state, dimensions, recorded_at
		 FROM billing_usage_records
		 WHERE tenant_id = ? AND meter_code = ? AND period_start = ? AND dimensions = ?`,
		tenantID, meterCode, start, dimensions)
	var u UsageRecord
	err := row.Scan(&u.ID, &u.TenantID, &u.MeterCode, &u.PeriodStart, &u.PeriodEnd,
		&u.Value, &u.Samples, &u.State, &u.Dimensions, &u.RecordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UsageRecord{}, ErrNotFound("usage record")
	}
	if err != nil {
		return UsageRecord{}, ErrInternal(err, "Could not read the usage record.")
	}
	return u, nil
}

// CurrentUsage reports how much of a resource a tenant holds right now. A
// counter registered by the owning subsystem is authoritative when one
// exists, because it counts the real rows rather than a derived tally;
// otherwise the meter for the resource is summed for the current period.
func (s *Service) CurrentUsage(ctx context.Context, tenantID, resource string) (int64, error) {
	if fn, ok := s.counterFor(resource); ok {
		used, err := fn(ctx, tenantID)
		if err != nil {
			return 0, ErrInternal(err, "Could not count current usage for "+resource+".")
		}
		return ClampNonNegative(used), nil
	}
	start, _ := s.CurrentPeriod(ctx, tenantID)
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT SUM(r.value) FROM billing_usage_records r
		 JOIN billing_usage_meters m ON m.code = r.meter_code
		 WHERE r.tenant_id = ? AND m.resource = ? AND r.period_start >= ?`,
		tenantID, resource, start).Scan(&total)
	if err != nil {
		return 0, ErrInternal(err, "Could not read current usage for "+resource+".")
	}
	if !total.Valid {
		return 0, nil
	}
	return ClampNonNegative(total.Int64), nil
}

// ListUsage returns a tenant's raw measurements for the current period.
func (s *Service) ListUsage(ctx context.Context, tenantID string) ([]UsageRecord, error) {
	start, _ := s.CurrentPeriod(ctx, tenantID)
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT id, tenant_id, meter_code, period_start, period_end, value,
		        samples, state, dimensions, recorded_at
		 FROM billing_usage_records
		 WHERE tenant_id = ? AND period_start >= ?
		 ORDER BY meter_code`, tenantID, start)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the usage records.")
	}
	defer func() { _ = rows.Close() }()

	out := []UsageRecord{}
	for rows.Next() {
		var u UsageRecord
		if err := rows.Scan(&u.ID, &u.TenantID, &u.MeterCode, &u.PeriodStart,
			&u.PeriodEnd, &u.Value, &u.Samples, &u.State, &u.Dimensions,
			&u.RecordedAt); err != nil {
			return nil, ErrInternal(err, "Could not read the usage records.")
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the usage records.")
	}
	return out, nil
}

// RollUpUsage totals a tenant's measurements for one period and splits them
// into what the plan includes and what is overage. The rollup is what the
// invoice generator reads; the raw records stay untouched for audit.
func (s *Service) RollUpUsage(ctx context.Context, tenantID string, start, end int64) ([]UsageRollup, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutReport,
		`SELECT r.meter_code, SUM(r.value), m.resource
		 FROM billing_usage_records r
		 JOIN billing_usage_meters m ON m.code = r.meter_code
		 WHERE r.tenant_id = ? AND r.period_start >= ? AND r.period_start < ?
		 GROUP BY r.meter_code, m.resource`, tenantID, start, end)
	if err != nil {
		return nil, ErrInternal(err, "Could not total the usage records.")
	}
	defer func() { _ = rows.Close() }()

	type totalRow struct {
		meter    string
		total    int64
		resource string
	}
	totals := []totalRow{}
	for rows.Next() {
		var t totalRow
		if err := rows.Scan(&t.meter, &t.total, &t.resource); err != nil {
			return nil, ErrInternal(err, "Could not total the usage records.")
		}
		totals = append(totals, t)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not total the usage records.")
	}

	now := s.unix()
	out := make([]UsageRollup, 0, len(totals))
	for _, t := range totals {
		included := t.total
		overage := int64(0)
		if t.resource != "" {
			quota, qErr := s.effectiveQuotaFor(ctx, tenantID, t.resource)
			if qErr != nil {
				return nil, qErr
			}
			if quota.limit != Unlimited && t.total > quota.limit {
				included = quota.limit
				overage = t.total - quota.limit
			}
		}
		rollup := UsageRollup{
			ID:            newID(),
			TenantID:      tenantID,
			MeterCode:     t.meter,
			PeriodStart:   start,
			PeriodEnd:     end,
			TotalValue:    t.total,
			IncludedValue: included,
			OverageValue:  overage,
			RolledAt:      now,
		}
		if err := s.saveRollup(ctx, rollup); err != nil {
			return nil, err
		}
		out = append(out, rollup)
	}
	return out, nil
}

// saveRollup writes one rollup row, replacing an earlier total for the same
// tenant, meter and period.
func (s *Service) saveRollup(ctx context.Context, r UsageRollup) error {
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE billing_usage_rollups SET total_value = ?, included_value = ?,
		   overage_value = ?, period_end = ?, rolled_at = ?
		 WHERE tenant_id = ? AND meter_code = ? AND period_start = ? AND invoiced = 0`,
		r.TotalValue, r.IncludedValue, r.OverageValue, r.PeriodEnd, r.RolledAt,
		r.TenantID, r.MeterCode, r.PeriodStart)
	if err != nil {
		return ErrInternal(err, "Could not save the usage rollup.")
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_usage_rollups
		 (id, tenant_id, meter_code, period_start, period_end, total_value,
		  included_value, overage_value, invoiced, rolled_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		r.ID, r.TenantID, r.MeterCode, r.PeriodStart, r.PeriodEnd, r.TotalValue,
		r.IncludedValue, r.OverageValue, r.RolledAt)
	if err != nil {
		return ErrInternal(err, "Could not save the usage rollup.")
	}
	return nil
}

// PendingRollups returns a tenant's un-invoiced rollups for a period.
func (s *Service) PendingRollups(ctx context.Context, tenantID string, start, end int64) ([]UsageRollup, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT id, tenant_id, meter_code, period_start, period_end, total_value,
		        included_value, overage_value, invoiced, rolled_at
		 FROM billing_usage_rollups
		 WHERE tenant_id = ? AND period_start >= ? AND period_start < ? AND invoiced = 0
		 ORDER BY meter_code`, tenantID, start, end)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the usage rollups.")
	}
	defer func() { _ = rows.Close() }()

	out := []UsageRollup{}
	for rows.Next() {
		var r UsageRollup
		var invoiced int64
		if err := rows.Scan(&r.ID, &r.TenantID, &r.MeterCode, &r.PeriodStart,
			&r.PeriodEnd, &r.TotalValue, &r.IncludedValue, &r.OverageValue,
			&invoiced, &r.RolledAt); err != nil {
			return nil, ErrInternal(err, "Could not read the usage rollups.")
		}
		r.Invoiced = invoiced != 0
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the usage rollups.")
	}
	return out, nil
}

// markRollupsInvoiced flags rollups as billed so the next invoice run cannot
// charge for the same usage twice.
func (s *Service) markRollupsInvoiced(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
			`UPDATE billing_usage_rollups SET invoiced = 1 WHERE id = ?`, id); err != nil {
			return ErrInternal(err, "Could not mark the usage as invoiced.")
		}
	}
	return nil
}
