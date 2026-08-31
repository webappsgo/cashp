package billing

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/webappsgo/cashp/src/database"
)

// planColumns is the explicit column list for billing_plans.
const planColumns = `id, code, name, description, currency, price_minor, cycle,
	trial_days, grace_days, visibility, overage_policy, active, sort_order,
	created_at, updated_at, version`

// scanPlan reads one billing_plans row in planColumns order.
func scanPlan(sc interface{ Scan(...any) error }) (Plan, error) {
	var p Plan
	var active int64
	if err := sc.Scan(&p.ID, &p.Code, &p.Name, &p.Description, &p.Currency,
		&p.PriceMinor, &p.Cycle, &p.TrialDays, &p.GraceDays, &p.Visibility,
		&p.OveragePolicy, &active, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt,
		&p.Version); err != nil {
		return Plan{}, err
	}
	p.Active = active != 0
	return p, nil
}

// planQuotaColumns is the explicit column list for billing_plan_quotas.
const planQuotaColumns = `id, plan_id, resource, limit_value, enforcement,
	burst_value, overage_unit_price_minor, updated_at`

// Plan returns one plan by identifier, with its quota ceilings attached.
func (s *Service) Plan(ctx context.Context, planID string) (Plan, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+planColumns+` FROM billing_plans WHERE id = ?`, planID)
	p, err := scanPlan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Plan{}, ErrNotFound("plan")
	}
	if err != nil {
		return Plan{}, ErrInternal(err, "Could not read the plan.")
	}
	quotas, err := s.PlanQuotas(ctx, p.ID)
	if err != nil {
		return Plan{}, err
	}
	p.Quotas = quotas
	return p, nil
}

// PlanByCode returns one plan by its stable operator-chosen code.
func (s *Service) PlanByCode(ctx context.Context, code string) (Plan, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+planColumns+` FROM billing_plans WHERE code = ?`,
		strings.ToLower(strings.TrimSpace(code)))
	p, err := scanPlan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Plan{}, ErrNotFound("plan")
	}
	if err != nil {
		return Plan{}, ErrInternal(err, "Could not read the plan.")
	}
	quotas, err := s.PlanQuotas(ctx, p.ID)
	if err != nil {
		return Plan{}, err
	}
	p.Quotas = quotas
	return p, nil
}

// ListPlans returns plans ordered for display. When onlyPublic is true the
// result is limited to what an unauthenticated visitor may see.
func (s *Service) ListPlans(ctx context.Context, onlyPublic bool) ([]Plan, error) {
	query := `SELECT ` + planColumns + ` FROM billing_plans`
	args := []any{}
	if onlyPublic {
		query += ` WHERE active = 1 AND visibility = ?`
		args = append(args, VisibilityPublic)
	}
	query += ` ORDER BY sort_order, price_minor, code`

	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query, args...)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the plan catalogue.")
	}
	defer func() { _ = rows.Close() }()

	plans := []Plan{}
	for rows.Next() {
		p, sErr := scanPlan(rows)
		if sErr != nil {
			return nil, ErrInternal(sErr, "Could not read the plan catalogue.")
		}
		plans = append(plans, p)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the plan catalogue.")
	}
	for i := range plans {
		quotas, qErr := s.PlanQuotas(ctx, plans[i].ID)
		if qErr != nil {
			return nil, qErr
		}
		plans[i].Quotas = quotas
	}
	return plans, nil
}

// PlanQuotas returns the ceilings attached to a plan.
func (s *Service) PlanQuotas(ctx context.Context, planID string) ([]PlanQuota, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+planQuotaColumns+` FROM billing_plan_quotas
		 WHERE plan_id = ? ORDER BY resource`, planID)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the plan quotas.")
	}
	defer func() { _ = rows.Close() }()

	out := []PlanQuota{}
	for rows.Next() {
		var q PlanQuota
		if err := rows.Scan(&q.ID, &q.PlanID, &q.Resource, &q.LimitValue,
			&q.Enforcement, &q.BurstValue, &q.OverageUnitPriceMinor,
			&q.UpdatedAt); err != nil {
			return nil, ErrInternal(err, "Could not read the plan quotas.")
		}
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the plan quotas.")
	}
	return out, nil
}

// validatePlan checks a plan's own fields before it is written.
func (s *Service) validatePlan(p *Plan) error {
	p.Code = strings.ToLower(strings.TrimSpace(p.Code))
	p.Name = strings.TrimSpace(p.Name)
	if p.Code == "" || p.Name == "" {
		return ErrValidation("A plan needs a code and a name.")
	}
	currency, err := NormalizeCurrency(p.Currency)
	if err != nil {
		return ErrValidation(err.Error())
	}
	p.Currency = currency
	if p.PriceMinor < 0 {
		return ErrValidation("A plan price cannot be negative.")
	}
	if !ValidCycle(p.Cycle) {
		return ErrValidation("That billing cycle is not one this server bills.")
	}
	if p.TrialDays < 0 || p.TrialDays > 365 {
		return ErrValidation("The trial length must be between 0 and 365 days.")
	}
	if p.GraceDays < 0 || p.GraceDays > 90 {
		return ErrValidation("The grace period must be between 0 and 90 days.")
	}
	if p.Visibility == "" {
		p.Visibility = VisibilityPublic
	}
	if !ValidVisibility(p.Visibility) {
		return ErrValidation("That plan visibility is not recognised.")
	}
	if p.OveragePolicy == "" {
		p.OveragePolicy = OverageBlock
	}
	switch p.OveragePolicy {
	case OverageBlock, OverageAllowWithCharge, OverageAllowWithWarning, OverageThrottle:
	default:
		return ErrValidation("That overage policy is not recognised.")
	}
	return s.validateQuotas(p.Quotas)
}

// validateQuotas checks the ceilings attached to a plan. A quota may only
// bound a quantity; there is deliberately no way to express "this feature is
// off", because every cashp feature is available on every plan.
func (s *Service) validateQuotas(quotas []PlanQuota) error {
	seen := make(map[string]bool, len(quotas))
	for _, q := range quotas {
		if !ValidResource(q.Resource) {
			return ErrValidation("Unknown quota resource " + q.Resource + ".")
		}
		if seen[q.Resource] {
			return ErrValidation("The quota for " + q.Resource + " is listed twice.")
		}
		seen[q.Resource] = true
		if q.LimitValue < Unlimited {
			return ErrValidation("A quota limit must be -1 for unlimited or zero or more.")
		}
		if q.Enforcement != "" && !ValidEnforcement(q.Enforcement) {
			return ErrValidation("That quota enforcement mode is not recognised.")
		}
		if q.BurstValue < 0 {
			return ErrValidation("A burst allowance cannot be negative.")
		}
		if q.OverageUnitPriceMinor < 0 {
			return ErrValidation("An overage price cannot be negative.")
		}
	}
	return nil
}

// CreatePlan adds a plan to the catalogue.
func (s *Service) CreatePlan(ctx context.Context, p Plan, actor, ip string) (Plan, error) {
	if err := s.validatePlan(&p); err != nil {
		return Plan{}, err
	}
	if _, err := s.PlanByCode(ctx, p.Code); err == nil {
		return Plan{}, ErrConflict("A plan with that code already exists.")
	} else if !isNotFound(err) {
		return Plan{}, err
	}

	now := s.unix()
	p.ID = newID()
	p.CreatedAt, p.UpdatedAt, p.Version = now, now, 1
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_plans
		 (id, code, name, description, currency, price_minor, cycle, trial_days,
		  grace_days, visibility, overage_policy, active, sort_order,
		  created_at, updated_at, version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		p.ID, p.Code, p.Name, p.Description, p.Currency, p.PriceMinor, p.Cycle,
		p.TrialDays, p.GraceDays, p.Visibility, p.OveragePolicy,
		boolToInt(p.Active), p.SortOrder, now, now)
	if err != nil {
		return Plan{}, ErrInternal(err, "Could not create the plan.")
	}
	if err := s.replaceQuotas(ctx, p.ID, p.Quotas); err != nil {
		return Plan{}, err
	}

	s.WriteAudit(ctx, AuditRecord{
		Actor: actor, Action: ActionPlanCreated, Target: "plan:" + p.Code, IP: ip,
	})
	return s.Plan(ctx, p.ID)
}

// UpdatePlan saves an existing plan. The price recorded on a live
// subscription is never touched: a catalogue edit applies to new
// subscriptions and to renewals the tenant is told about in advance.
func (s *Service) UpdatePlan(ctx context.Context, planID string, p Plan, actor, ip string) (Plan, error) {
	current, err := s.Plan(ctx, planID)
	if err != nil {
		return Plan{}, err
	}
	p.Code = current.Code
	if err := s.validatePlan(&p); err != nil {
		return Plan{}, err
	}

	now := s.unix()
	err = s.db.UpdateVersioned(ctx,
		`UPDATE billing_plans SET
		   name = ?, description = ?, currency = ?, price_minor = ?, cycle = ?,
		   trial_days = ?, grace_days = ?, visibility = ?, overage_policy = ?,
		   active = ?, sort_order = ?, updated_at = ?, version = version + 1
		 WHERE id = ? AND version = ?`,
		p.Name, p.Description, p.Currency, p.PriceMinor, p.Cycle, p.TrialDays,
		p.GraceDays, p.Visibility, p.OveragePolicy, boolToInt(p.Active),
		p.SortOrder, now, planID, current.Version)
	if err != nil {
		if database.IsConflict(err) {
			return Plan{}, ErrConflict("The plan changed while you were editing it; reload and try again.")
		}
		return Plan{}, ErrInternal(err, "Could not save the plan.")
	}
	if err := s.replaceQuotas(ctx, planID, p.Quotas); err != nil {
		return Plan{}, err
	}

	s.WriteAudit(ctx, AuditRecord{
		Actor: actor, Action: ActionPlanUpdated, Target: "plan:" + current.Code, IP: ip,
	})
	return s.Plan(ctx, planID)
}

// replaceQuotas writes a plan's ceilings, updating rows that exist and
// inserting the rest. A ceiling that is no longer listed is set to unlimited
// rather than deleted, because deleting it would silently start enforcing a
// zero limit against tenants already on the plan.
func (s *Service) replaceQuotas(ctx context.Context, planID string, quotas []PlanQuota) error {
	existing, err := s.PlanQuotas(ctx, planID)
	if err != nil {
		return err
	}
	byResource := make(map[string]PlanQuota, len(existing))
	for _, q := range existing {
		byResource[q.Resource] = q
	}
	now := s.unix()
	wanted := make(map[string]bool, len(quotas))

	for _, q := range quotas {
		wanted[q.Resource] = true
		if q.Enforcement == "" {
			q.Enforcement = EnforceHard
		}
		if prior, ok := byResource[q.Resource]; ok {
			if _, uErr := s.db.ExecContext(ctx, database.TimeoutWrite,
				`UPDATE billing_plan_quotas SET limit_value = ?, enforcement = ?,
				   burst_value = ?, overage_unit_price_minor = ?, updated_at = ?
				 WHERE id = ?`,
				q.LimitValue, q.Enforcement, q.BurstValue,
				q.OverageUnitPriceMinor, now, prior.ID); uErr != nil {
				return ErrInternal(uErr, "Could not save the plan quotas.")
			}
			continue
		}
		if _, iErr := s.db.ExecContext(ctx, database.TimeoutWrite,
			`INSERT INTO billing_plan_quotas
			 (id, plan_id, resource, limit_value, enforcement, burst_value,
			  overage_unit_price_minor, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			newID(), planID, q.Resource, q.LimitValue, q.Enforcement,
			q.BurstValue, q.OverageUnitPriceMinor, now); iErr != nil {
			return ErrInternal(iErr, "Could not save the plan quotas.")
		}
	}

	for _, prior := range existing {
		if wanted[prior.Resource] || prior.LimitValue == Unlimited {
			continue
		}
		if _, uErr := s.db.ExecContext(ctx, database.TimeoutWrite,
			`UPDATE billing_plan_quotas SET limit_value = ?, updated_at = ?
			 WHERE id = ?`, Unlimited, now, prior.ID); uErr != nil {
			return ErrInternal(uErr, "Could not save the plan quotas.")
		}
	}
	return nil
}
