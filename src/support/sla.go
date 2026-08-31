package support

import (
	"context"
	"time"
)

// DefaultSLAPolicies are the response and resolution allowances a fresh
// installation starts with. They are seeded into support_sla_policies on first
// start and are edited from the admin panel afterwards; nothing here reads an
// environment variable.
var DefaultSLAPolicies = []SLAPolicy{
	{Priority: PriorityUrgent, FirstResponseMins: 60, ResolutionMins: 240, EscalatePercent: 80, Enabled: true},
	{Priority: PriorityHigh, FirstResponseMins: 240, ResolutionMins: 1440, EscalatePercent: 80, Enabled: true},
	{Priority: PriorityNormal, FirstResponseMins: 1440, ResolutionMins: 4320, EscalatePercent: 80, Enabled: true},
	{Priority: PriorityLow, FirstResponseMins: 4320, ResolutionMins: 10080, EscalatePercent: 80, Enabled: true},
}

// SLARisk is one ticket's standing against its policy.
type SLARisk struct {
	// Policy is the policy the ticket is measured against.
	Policy SLAPolicy
	// ResponseElapsed is the fraction of the first-response allowance used,
	// expressed as a percentage. It stops counting once an agent has replied.
	ResponseElapsed int
	// ResolutionElapsed is the fraction of the resolution allowance used.
	ResolutionElapsed int
	// ResponseBreached is true once the first-response allowance ran out with
	// no agent reply.
	ResponseBreached bool
	// ResolutionBreached is true once the resolution allowance ran out with
	// the ticket still open.
	ResolutionBreached bool
	// Escalate is true once either clock passed the policy's escalation point.
	Escalate bool
	// Level is a coarse indicator for the queue: "ok", "warn", or "breach".
	Level string
}

// EvaluateSLA measures a ticket against a policy at a point in time. A ticket
// that has not entered the queue, or that is already closed, carries no risk.
func EvaluateSLA(t Ticket, p SLAPolicy, at int64) SLARisk {
	risk := SLARisk{Policy: p, Level: "ok"}
	if !p.Enabled || t.CreatedAt <= 0 || !IsOpenState(t.Status) {
		return risk
	}

	escalateAt := p.EscalatePercent
	if escalateAt <= 0 || escalateAt > 100 {
		escalateAt = 80
	}

	if p.FirstResponseMins > 0 {
		if t.FirstResponseAt > 0 {
			risk.ResponseElapsed = percentOf(t.FirstResponseAt-t.CreatedAt, p.FirstResponseMins)
			risk.ResponseBreached = risk.ResponseElapsed >= 100
		} else {
			risk.ResponseElapsed = percentOf(at-t.CreatedAt, p.FirstResponseMins)
			risk.ResponseBreached = risk.ResponseElapsed >= 100
		}
	}
	if p.ResolutionMins > 0 {
		risk.ResolutionElapsed = percentOf(at-t.CreatedAt, p.ResolutionMins)
		risk.ResolutionBreached = risk.ResolutionElapsed >= 100
	}

	worst := risk.ResponseElapsed
	if risk.ResolutionElapsed > worst {
		worst = risk.ResolutionElapsed
	}
	switch {
	case risk.ResponseBreached || risk.ResolutionBreached:
		risk.Level = "breach"
		risk.Escalate = true
	case worst >= escalateAt:
		risk.Level = "warn"
		risk.Escalate = true
	}
	return risk
}

// percentOf expresses an elapsed number of seconds as a percentage of an
// allowance given in minutes. A non-positive allowance means "no limit".
func percentOf(elapsedSeconds int64, allowanceMinutes int) int {
	if allowanceMinutes <= 0 {
		return 0
	}
	if elapsedSeconds < 0 {
		elapsedSeconds = 0
	}
	allowance := int64(allowanceMinutes) * 60
	return int((elapsedSeconds * 100) / allowance)
}

// SLADeadline returns the wall-clock instants a ticket must be answered and
// resolved by.
func SLADeadline(t Ticket, p SLAPolicy) (firstResponse, resolution time.Time) {
	created := time.Unix(t.CreatedAt, 0).UTC()
	return created.Add(time.Duration(p.FirstResponseMins) * time.Minute),
		created.Add(time.Duration(p.ResolutionMins) * time.Minute)
}

// SLAPolicies loads the configured policies keyed by priority, falling back to
// the defaults for any priority that has no row yet.
func (s *Service) SLAPolicies(ctx context.Context) (map[string]SLAPolicy, error) {
	byPriority := map[string]SLAPolicy{}
	for _, p := range DefaultSLAPolicies {
		byPriority[p.Priority] = p
	}
	stored, err := s.store.ListSLAPolicies(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range stored {
		if IsPriority(p.Priority) {
			byPriority[p.Priority] = p
		}
	}
	return byPriority, nil
}

// PolicyFor returns the policy that applies to one ticket.
func (s *Service) PolicyFor(ctx context.Context, t Ticket) (SLAPolicy, error) {
	policies, err := s.SLAPolicies(ctx)
	if err != nil {
		return SLAPolicy{}, err
	}
	p, ok := policies[t.Priority]
	if !ok {
		p = policies[PriorityNormal]
	}
	return p, nil
}

// seedSLAPolicies writes the default policies for any priority that has none.
func (s *Service) seedSLAPolicies(ctx context.Context) error {
	stored, err := s.store.ListSLAPolicies(ctx)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, p := range stored {
		have[p.Priority] = true
	}
	for _, p := range DefaultSLAPolicies {
		if have[p.Priority] {
			continue
		}
		p.ID = newID("sla")
		p.UpdatedAt = now()
		if err := s.store.UpsertSLAPolicy(ctx, p); err != nil {
			return err
		}
	}
	return nil
}
