package billing

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/webappsgo/cashp/src/database"
)

// Quota is the one place resource ceilings are decided. Every subsystem that
// provisions something asks here first and nothing else in cashp reimplements
// the arithmetic.
//
// Two rules hold everywhere in this file. A quota bounds a quantity and can
// never remove a capability: there is no code path by which a plan turns a
// product feature off, only one by which it caps how many of a thing a tenant
// may hold. And an unconfigured server has no quotas at all, so a fresh
// install provisions freely until an operator sets billing up.

// Sources a ceiling can come from, reported so the UI can explain a refusal.
const (
	SourcePlan      = "plan"
	SourceOverride  = "override"
	SourceUnlimited = "unlimited"
)

// QuotaDecision is the answer to a single provisioning question.
type QuotaDecision struct {
	Resource    string `json:"resource"`
	Allowed     bool   `json:"allowed"`
	LimitValue  int64  `json:"limit_value"`
	Used        int64  `json:"used"`
	Requested   int64  `json:"requested"`
	Remaining   int64  `json:"remaining"`
	Overage     int64  `json:"overage"`
	Enforcement string `json:"enforcement"`
	Source      string `json:"source"`
	Unlimited   bool   `json:"unlimited"`
	Reason      string `json:"reason,omitempty"`
}

// effectiveQuota is a resolved ceiling for one tenant and resource.
type effectiveQuota struct {
	limit       int64
	burst       int64
	enforcement string
	source      string
	overagePol  string
	unitPrice   int64
}

// unlimitedQuota is the ceiling used when nothing constrains a resource.
func unlimitedQuota() effectiveQuota {
	return effectiveQuota{
		limit:       Unlimited,
		enforcement: EnforceSoft,
		source:      SourceUnlimited,
		overagePol:  OverageAllowWithWarning,
	}
}

// EnsureQuota is the guard other packages call immediately before creating a
// resource. It returns nil when the tenant may proceed and a QUOTA_EXCEEDED
// error carrying the limit, the current usage and the unit when they may not.
//
//	if err := billing.EnsureQuota(ctx, orgID, billing.ResourceSites, 1); err != nil {
//	        return err
//	}
//
// Call it before the resource is created, never after, and never as a
// substitute for the caller's own authorization check.
func (s *Service) EnsureQuota(ctx context.Context, tenantID, resource string, requested int64) error {
	decision, err := s.CheckQuota(ctx, tenantID, resource, requested)
	if err != nil {
		return err
	}
	if decision.Allowed {
		return nil
	}
	s.WriteAudit(ctx, AuditRecord{
		TenantID: tenantID, Action: ActionQuotaDenied,
		Target: "resource:" + resource, Result: ResultDenied,
		Detail: "limit=" + itoa(decision.LimitValue) + " used=" + itoa(decision.Used) +
			" requested=" + itoa(requested),
	})
	return ErrQuota(resource, decision.LimitValue, decision.Used, requested)
}

// CheckQuota answers whether a tenant may consume more of a resource without
// recording anything. Use EnsureQuota at a provisioning site; this method is
// for dashboards and previews.
func (s *Service) CheckQuota(ctx context.Context, tenantID, resource string, requested int64) (QuotaDecision, error) {
	if strings.TrimSpace(tenantID) == "" {
		return QuotaDecision{}, ErrValidation("billing: a tenant is required")
	}
	if !ValidResource(resource) {
		return QuotaDecision{}, ErrValidation("billing: unknown quota resource " + resource)
	}
	if requested < 0 {
		return QuotaDecision{}, ErrValidation("billing: a quota request cannot be negative")
	}

	quota, err := s.effectiveQuotaFor(ctx, tenantID, resource)
	if err != nil {
		return QuotaDecision{}, err
	}
	used, err := s.CurrentUsage(ctx, tenantID, resource)
	if err != nil {
		return QuotaDecision{}, err
	}
	return decide(resource, quota, used, requested), nil
}

// decide applies an enforcement strategy to a resolved ceiling. It is pure
// integer arithmetic with no I/O so the boundary behaviour is directly
// testable.
func decide(resource string, quota effectiveQuota, used, requested int64) QuotaDecision {
	d := QuotaDecision{
		Resource:    resource,
		LimitValue:  quota.limit,
		Used:        used,
		Requested:   requested,
		Enforcement: quota.enforcement,
		Source:      quota.source,
	}
	if quota.limit == Unlimited {
		d.Allowed = true
		d.Unlimited = true
		d.Remaining = Unlimited
		return d
	}

	after := used + requested
	d.Remaining = ClampNonNegative(quota.limit - used)
	if after <= quota.limit {
		d.Allowed = true
		return d
	}
	d.Overage = after - quota.limit

	switch quota.enforcement {
	case EnforceSoft:
		// A soft ceiling never blocks; the excess is recorded and billed or
		// warned about according to the plan's overage policy.
		d.Allowed = quota.overagePol != OverageBlock
		if !d.Allowed {
			d.Reason = "The plan's overage policy blocks usage past the limit."
		}
	case EnforceBurst:
		d.Allowed = after <= quota.limit+quota.burst
		if !d.Allowed {
			d.Reason = "The burst allowance for this resource is exhausted."
		}
	default:
		d.Allowed = false
		d.Reason = "The plan limit for this resource has been reached."
	}
	return d
}

// effectiveQuotaFor resolves the ceiling that applies to one tenant and
// resource: an unexpired administrator override first, then the plan on the
// tenant's entitling subscription, then no ceiling at all.
func (s *Service) effectiveQuotaFor(ctx context.Context, tenantID, resource string) (effectiveQuota, error) {
	override, err := s.quotaOverride(ctx, tenantID, resource)
	switch {
	case err == nil:
		enforcement := override.Enforcement
		if !ValidEnforcement(enforcement) {
			enforcement = EnforceHard
		}
		return effectiveQuota{
			limit:       override.LimitValue,
			enforcement: enforcement,
			source:      SourceOverride,
			overagePol:  OverageAllowWithWarning,
		}, nil
	case !isNotFound(err):
		return effectiveQuota{}, err
	}

	sub, err := s.ActiveSubscription(ctx, tenantID)
	if isNotFound(err) {
		// No entitling subscription means no plan and therefore no ceiling:
		// cashp does not withhold capability from an unbilled tenant.
		return unlimitedQuota(), nil
	}
	if err != nil {
		return effectiveQuota{}, err
	}

	quotas, err := s.PlanQuotas(ctx, sub.PlanID)
	if err != nil {
		return effectiveQuota{}, err
	}
	plan, err := s.Plan(ctx, sub.PlanID)
	if err != nil {
		return effectiveQuota{}, err
	}
	for _, q := range quotas {
		if q.Resource != resource {
			continue
		}
		enforcement := q.Enforcement
		if !ValidEnforcement(enforcement) {
			enforcement = EnforceHard
		}
		return effectiveQuota{
			limit:       q.LimitValue,
			burst:       q.BurstValue,
			enforcement: enforcement,
			source:      SourcePlan,
			overagePol:  plan.OveragePolicy,
			unitPrice:   q.OverageUnitPriceMinor,
		}, nil
	}
	return unlimitedQuota(), nil
}

// quotaOverride reads the newest unexpired administrator override for a
// tenant and resource.
func (s *Service) quotaOverride(ctx context.Context, tenantID, resource string) (QuotaOverride, error) {
	now := s.unix()
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT id, tenant_id, resource, limit_value, enforcement, reason,
		        created_by, created_at, expires_at
		 FROM billing_quota_overrides
		 WHERE tenant_id = ? AND resource = ? AND (expires_at = 0 OR expires_at > ?)
		 ORDER BY created_at DESC LIMIT 1`, tenantID, resource, now)
	var o QuotaOverride
	err := row.Scan(&o.ID, &o.TenantID, &o.Resource, &o.LimitValue, &o.Enforcement,
		&o.Reason, &o.CreatedBy, &o.CreatedAt, &o.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return QuotaOverride{}, ErrNotFound("quota override")
	}
	if err != nil {
		return QuotaOverride{}, ErrInternal(err, "Could not read the quota overrides.")
	}
	return o, nil
}

// SetQuotaOverride records an administrator override for one tenant and
// resource. Only a global administrator reaches this, and every call is
// audited with the reason the operator gave.
func (s *Service) SetQuotaOverride(ctx context.Context, o QuotaOverride, actor, ip string) (QuotaOverride, error) {
	if strings.TrimSpace(o.TenantID) == "" {
		return QuotaOverride{}, ErrValidation("An override needs a tenant.")
	}
	if !ValidResource(o.Resource) {
		return QuotaOverride{}, ErrValidation("Unknown quota resource " + o.Resource + ".")
	}
	if o.LimitValue < Unlimited {
		return QuotaOverride{}, ErrValidation("A quota limit must be -1 for unlimited or zero or more.")
	}
	if strings.TrimSpace(o.Reason) == "" {
		return QuotaOverride{}, ErrValidation("An override needs a reason for the audit trail.")
	}
	if o.Enforcement == "" {
		o.Enforcement = EnforceHard
	}
	if !ValidEnforcement(o.Enforcement) {
		return QuotaOverride{}, ErrValidation("That quota enforcement mode is not recognised.")
	}

	o.ID = newID()
	o.CreatedBy = actor
	o.CreatedAt = s.unix()
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO billing_quota_overrides
		 (id, tenant_id, resource, limit_value, enforcement, reason, created_by,
		  created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.ID, o.TenantID, o.Resource, o.LimitValue, o.Enforcement, o.Reason,
		o.CreatedBy, o.CreatedAt, o.ExpiresAt)
	if err != nil {
		return QuotaOverride{}, ErrInternal(err, "Could not record the quota override.")
	}

	s.WriteAudit(ctx, AuditRecord{
		TenantID: o.TenantID, Actor: actor, Action: ActionQuotaOverride,
		Target: "resource:" + o.Resource, IP: ip,
		Detail: "limit=" + itoa(o.LimitValue) + " reason=" + o.Reason,
	})
	return o, nil
}

// ListQuotaOverrides returns the overrides recorded for one tenant.
func (s *Service) ListQuotaOverrides(ctx context.Context, tenantID string) ([]QuotaOverride, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT id, tenant_id, resource, limit_value, enforcement, reason,
		        created_by, created_at, expires_at
		 FROM billing_quota_overrides WHERE tenant_id = ?
		 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, ErrInternal(err, "Could not read the quota overrides.")
	}
	defer func() { _ = rows.Close() }()

	out := []QuotaOverride{}
	for rows.Next() {
		var o QuotaOverride
		if err := rows.Scan(&o.ID, &o.TenantID, &o.Resource, &o.LimitValue,
			&o.Enforcement, &o.Reason, &o.CreatedBy, &o.CreatedAt,
			&o.ExpiresAt); err != nil {
			return nil, ErrInternal(err, "Could not read the quota overrides.")
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrInternal(err, "Could not read the quota overrides.")
	}
	return out, nil
}

// QuotaStatuses returns every resource's ceiling and consumption for one
// tenant, which is what the usage dashboard and the quota API both render.
func (s *Service) QuotaStatuses(ctx context.Context, tenantID string) ([]QuotaStatus, error) {
	resources := Resources()
	out := make([]QuotaStatus, 0, len(resources))
	for _, resource := range resources {
		quota, err := s.effectiveQuotaFor(ctx, tenantID, resource)
		if err != nil {
			return nil, err
		}
		used, err := s.CurrentUsage(ctx, tenantID, resource)
		if err != nil {
			return nil, err
		}
		status := QuotaStatus{
			Resource:    resource,
			LimitValue:  quota.limit,
			Used:        used,
			Enforcement: quota.enforcement,
			Source:      quota.source,
			Unit:        ResourceUnit(resource),
			Unlimited:   quota.limit == Unlimited,
		}
		if status.Unlimited {
			status.Remaining = Unlimited
		} else {
			status.Remaining = ClampNonNegative(quota.limit - used)
		}
		out = append(out, status)
	}
	return out, nil
}

// EnsureQuota is the package-level guard. It delegates to the process-wide
// service and allows the operation outright when billing has never been
// configured, which is what lets a fresh install provision without any
// billing setup at all.
func EnsureQuota(ctx context.Context, tenantID, resource string, requested int64) error {
	svc := Default()
	if svc == nil {
		return nil
	}
	return svc.EnsureQuota(ctx, tenantID, resource, requested)
}

// CheckQuota is the package-level read-only form of EnsureQuota.
func CheckQuota(ctx context.Context, tenantID, resource string, requested int64) (QuotaDecision, error) {
	svc := Default()
	if svc == nil {
		return QuotaDecision{
			Resource: resource, Allowed: true, Unlimited: true,
			LimitValue: Unlimited, Remaining: Unlimited,
			Requested: requested, Source: SourceUnlimited,
		}, nil
	}
	return svc.CheckQuota(ctx, tenantID, resource, requested)
}
