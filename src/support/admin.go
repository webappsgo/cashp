package support

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/webappsgo/cashp/src/errors"
)

// requireAdmin insists the caller is a system administrator working in support
// mode. Configuration is an administrator's job and every change it makes is
// written to the append-only audit log.
func (s *Service) requireAdmin(ctx context.Context, id Identity) error {
	if _, err := s.requireSupportMode(ctx, id); err != nil {
		return err
	}
	if s.RoleOf(ctx, id) != RoleAdmin {
		return errors.New(errors.CodeForbidden, 403, "Permission denied")
	}
	return nil
}

// Settings returns the support configuration for the admin panel.
func (s *Service) Settings(ctx context.Context, id Identity) ([]Setting, error) {
	if err := s.requireAdmin(ctx, id); err != nil {
		return nil, err
	}
	stored, err := s.store.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, st := range stored {
		have[st.Key] = true
	}
	for key, value := range defaultSettings {
		if !have[key] {
			stored = append(stored, Setting{Key: key, Value: value})
		}
	}
	sort.Slice(stored, func(i, j int) bool { return stored[i].Key < stored[j].Key })
	return stored, nil
}

// SetSetting writes one support setting. Only the keys this package defines are
// accepted, so the settings table cannot be used as a general key-value store.
func (s *Service) SetSetting(ctx context.Context, id Identity, key, value string) error {
	if err := s.requireAdmin(ctx, id); err != nil {
		return err
	}
	key = clean(key)
	if _, known := defaultSettings[key]; !known {
		return errors.New(errors.CodeValidation, 400, "Unknown setting").
			WithDetails(map[string]any{"field": "key"})
	}
	value = truncate(clean(value), 500)
	if err := s.store.SetSetting(ctx, key, value, s.nowUnix()); err != nil {
		return err
	}
	return s.audit(ctx, id, AuditEntry{
		Action:     "config.set",
		EntityType: "setting",
		EntityID:   key,
		Detail:     key + " = " + value,
	})
}

// SaveSLAPolicy stores one priority's response and resolution allowances.
func (s *Service) SaveSLAPolicy(ctx context.Context, id Identity, p SLAPolicy) (SLAPolicy, error) {
	if err := s.requireAdmin(ctx, id); err != nil {
		return SLAPolicy{}, err
	}
	p.Priority = strings.ToUpper(clean(p.Priority))
	if !IsPriority(p.Priority) {
		return SLAPolicy{}, errors.New(errors.CodeValidation, 400, "Unknown priority").
			WithDetails(map[string]any{"field": "priority"})
	}
	if p.FirstResponseMins < 0 || p.ResolutionMins < 0 {
		return SLAPolicy{}, errors.New(errors.CodeValidation, 400, "An allowance cannot be negative").
			WithDetails(map[string]any{"field": "first_response_minutes"})
	}
	if p.EscalatePercent <= 0 || p.EscalatePercent > 100 {
		p.EscalatePercent = 80
	}

	existing, err := s.store.ListSLAPolicies(ctx)
	if err != nil {
		return SLAPolicy{}, err
	}
	for _, e := range existing {
		if e.Priority == p.Priority {
			p.ID = e.ID
			break
		}
	}
	if p.ID == "" {
		p.ID = newID("sla")
	}
	p.UpdatedAt = s.nowUnix()
	if err := s.store.UpsertSLAPolicy(ctx, p); err != nil {
		return SLAPolicy{}, err
	}
	if err := s.audit(ctx, id, AuditEntry{
		Action:     "config.sla",
		EntityType: "sla_policy",
		EntityID:   p.ID,
		Detail:     p.Priority,
	}); err != nil {
		return SLAPolicy{}, err
	}
	return p, nil
}

// SaveDepartment creates or updates a department.
func (s *Service) SaveDepartment(ctx context.Context, id Identity, d Department) (Department, error) {
	if err := s.requireAdmin(ctx, id); err != nil {
		return Department{}, err
	}
	d.Name = truncate(clean(d.Name), 120)
	d.Description = truncate(clean(d.Description), 400)
	if d.Name == "" {
		return Department{}, errors.New(errors.CodeValidation, 400, "A department needs a name").
			WithDetails(map[string]any{"field": "name"})
	}
	at := s.nowUnix()
	if d.ID == "" {
		d.ID = newID("dep")
		d.CreatedAt = at
	}
	d.UpdatedAt = at
	if err := s.store.UpsertDepartment(ctx, d); err != nil {
		return Department{}, err
	}
	if err := s.audit(ctx, id, AuditEntry{
		Action:     "config.department",
		EntityType: "department",
		EntityID:   d.ID,
		Detail:     d.Name,
	}); err != nil {
		return Department{}, err
	}
	return d, nil
}

// Departments lists every department for the admin panel.
func (s *Service) Departments(ctx context.Context, id Identity) ([]Department, error) {
	if _, err := s.requireAgent(ctx, id); err != nil {
		return nil, err
	}
	return s.store.ListDepartments(ctx)
}

// SaveCategory creates or updates a ticket category. Categories form a tree; a
// category may not be its own parent.
func (s *Service) SaveCategory(ctx context.Context, id Identity, c Category) (Category, error) {
	if err := s.requireAdmin(ctx, id); err != nil {
		return Category{}, err
	}
	c.Name = truncate(clean(c.Name), 120)
	if c.Name == "" {
		return Category{}, errors.New(errors.CodeValidation, 400, "A category needs a name").
			WithDetails(map[string]any{"field": "name"})
	}
	c.Slug = Slugify(firstNonEmpty(c.Slug, c.Name))
	if c.ID != "" && c.ParentID == c.ID {
		return Category{}, errors.New(errors.CodeValidation, 400, "A category cannot be its own parent").
			WithDetails(map[string]any{"field": "parent_id"})
	}
	if c.ID == "" {
		c.ID = newID("cat")
	}
	if err := s.store.UpsertCategory(ctx, c); err != nil {
		return Category{}, err
	}
	if err := s.audit(ctx, id, AuditEntry{
		Action:     "config.category",
		EntityType: "category",
		EntityID:   c.ID,
		Detail:     c.Name,
	}); err != nil {
		return Category{}, err
	}
	return c, nil
}

// Categories lists the enabled category tree. It is readable by anyone who can
// reach the ticket form, because the form has to offer the choices.
func (s *Service) Categories(ctx context.Context) ([]Category, error) {
	return s.store.ListCategories(ctx, true)
}

// SaveAgent creates or updates a support agent profile.
func (s *Service) SaveAgent(ctx context.Context, id Identity, a Agent) (Agent, error) {
	if err := s.requireAdmin(ctx, id); err != nil {
		return Agent{}, err
	}
	if a.UserID == 0 {
		return Agent{}, errors.New(errors.CodeValidation, 400, "An agent needs an account").
			WithDetails(map[string]any{"field": "user_id"})
	}
	a.DisplayName = truncate(clean(a.DisplayName), 120)
	if a.DisplayName == "" {
		return Agent{}, errors.New(errors.CodeValidation, 400, "An agent needs a display name").
			WithDetails(map[string]any{"field": "display_name"})
	}
	if a.MaxConcurrentChats <= 0 {
		a.MaxConcurrentChats = 3
	}

	at := s.nowUnix()
	if existing, err := s.store.AgentByUser(ctx, a.UserID); err == nil {
		a.ID = existing.ID
		a.CreatedAt = existing.CreatedAt
		a.LastActivityAt = existing.LastActivityAt
	} else {
		a.ID = newID("agt")
		a.CreatedAt = at
	}
	a.UpdatedAt = at
	if err := s.store.UpsertAgent(ctx, a); err != nil {
		return Agent{}, err
	}
	if err := s.audit(ctx, id, AuditEntry{
		Action:     "config.agent",
		EntityType: "agent",
		EntityID:   a.ID,
		Detail:     "user " + strconv.FormatInt(a.UserID, 10),
	}); err != nil {
		return Agent{}, err
	}
	return a, nil
}

// Agents lists every agent profile for the admin panel.
func (s *Service) Agents(ctx context.Context, id Identity) ([]Agent, error) {
	if err := s.requireAdmin(ctx, id); err != nil {
		return nil, err
	}
	return s.store.ListAgents(ctx, false)
}

// SaveSharedCanned creates or updates a SYSTEM or DEPARTMENT canned response.
// Only an administrator may write at these scopes; an agent writing their own
// goes through CreatePersonalCanned instead.
func (s *Service) SaveSharedCanned(ctx context.Context, id Identity, cannedID string, in CannedInput) (CannedResponse, error) {
	if err := s.requireAdmin(ctx, id); err != nil {
		return CannedResponse{}, err
	}
	in = in.normalize()
	if in.Scope == CannedPersonal {
		return CannedResponse{}, errors.New(errors.CodeValidation, 400,
			"Personal responses belong to the agent who wrote them").
			WithDetails(map[string]any{"field": "scope"})
	}
	if err := in.validate(); err != nil {
		return CannedResponse{}, err
	}

	at := s.nowUnix()
	c := CannedResponse{
		ID:           cannedID,
		Scope:        in.Scope,
		DepartmentID: in.DepartmentID,
		Title:        in.Title,
		Body:         in.Body,
		Tags:         in.Tags,
		UpdatedAt:    at,
	}
	if c.Scope == CannedSystem {
		c.DepartmentID = ""
	}
	if c.ID == "" {
		c.ID = newID("can")
		c.CreatedAt = at
		if err := s.store.InsertCanned(ctx, c); err != nil {
			return CannedResponse{}, err
		}
	} else {
		existing, err := s.store.Canned(ctx, c.ID)
		if err != nil {
			return CannedResponse{}, err
		}
		if existing.Scope == CannedPersonal {
			return CannedResponse{}, notFound("Canned response")
		}
		c.CreatedAt = existing.CreatedAt
		c.UsageCount = existing.UsageCount
		if err := s.store.UpdateCanned(ctx, c); err != nil {
			return CannedResponse{}, err
		}
	}
	if err := s.audit(ctx, id, AuditEntry{
		Action:     "config.canned",
		EntityType: "canned_response",
		EntityID:   c.ID,
		Detail:     c.Scope,
	}); err != nil {
		return CannedResponse{}, err
	}
	return c, nil
}

// SharedCanned lists the SYSTEM and DEPARTMENT responses for the admin panel.
// Personal responses are excluded: they belong to the agents who wrote them and
// an administrator has no reason to read them.
func (s *Service) SharedCanned(ctx context.Context, id Identity) ([]CannedResponse, error) {
	if err := s.requireAdmin(ctx, id); err != nil {
		return nil, err
	}
	return s.store.ListCannedAdmin(ctx)
}

// Report is one support report.
type Report struct {
	Name             string
	Counts           map[string]int
	SLACompliant     int
	SLABreached      int
	SatisfactionN    int
	SatisfactionAvg  int
	FirstContactRate int
}

// Reports builds one of the named support reports.
func (s *Service) Reports(ctx context.Context, id Identity, name string) (Report, error) {
	if err := s.requireAdmin(ctx, id); err != nil {
		return Report{}, err
	}
	switch name {
	case "volume":
		counts, err := s.store.GroupTickets(ctx, "status")
		if err != nil {
			return Report{}, err
		}
		return Report{Name: name, Counts: counts}, nil
	case "categories":
		counts, err := s.store.GroupTickets(ctx, "category")
		if err != nil {
			return Report{}, err
		}
		return Report{Name: name, Counts: counts}, nil
	case "priorities":
		counts, err := s.store.GroupTickets(ctx, "priority")
		if err != nil {
			return Report{}, err
		}
		return Report{Name: name, Counts: counts}, nil
	case "satisfaction":
		n, avg, err := s.store.ChatSatisfaction(ctx)
		if err != nil {
			return Report{}, err
		}
		return Report{Name: name, SatisfactionN: n, SatisfactionAvg: avg}, nil
	case "sla":
		return s.slaReport(ctx)
	case "agents":
		return s.agentReport(ctx)
	default:
		return Report{}, errors.New(errors.CodeNotFound, 404, "Unknown report")
	}
}

// slaReport measures how many open tickets are inside their allowance.
func (s *Service) slaReport(ctx context.Context) (Report, error) {
	tickets, err := s.store.TicketsInState(ctx, StateOpen, StateAssigned, StateInProgress,
		StateAwaitingUser, StateAwaitingAgent)
	if err != nil {
		return Report{}, err
	}
	policies, err := s.SLAPolicies(ctx)
	if err != nil {
		return Report{}, err
	}
	at := s.nowUnix()
	report := Report{Name: "sla", Counts: map[string]int{}}
	for _, t := range tickets {
		policy, ok := policies[t.Priority]
		if !ok {
			policy = policies[PriorityNormal]
		}
		risk := EvaluateSLA(t, policy, at)
		report.Counts[risk.Level]++
		if risk.Level == "breach" {
			report.SLABreached++
			continue
		}
		report.SLACompliant++
	}
	return report, nil
}

// agentReport counts the tickets each agent currently carries, keyed by the
// agent's display name so the report reads the same way customers see them.
func (s *Service) agentReport(ctx context.Context) (Report, error) {
	agents, err := s.store.ListAgents(ctx, false)
	if err != nil {
		return Report{}, err
	}
	report := Report{Name: "agents", Counts: map[string]int{}}
	for _, a := range agents {
		count, err := s.store.CountTickets(ctx, TicketFilter{AssignedTo: a.UserID, QueueOnly: true})
		if err != nil {
			return Report{}, err
		}
		report.Counts[a.DisplayName] = count
	}
	return report, nil
}
