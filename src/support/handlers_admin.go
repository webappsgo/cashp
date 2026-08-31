package support

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

// This file holds the administrator's side of support: the settings, the SLA
// tiers, the departments, the category tree, the agent roster, the shared
// canned responses and the reports. Every setting lives in the database and is
// edited here through the web interface; nothing in this subsystem reads an
// environment variable.

// Default form values, mirroring the zero-value fallbacks applied in
// admin.go (SLAPolicy.EscalatePercent, Agent.MaxConcurrentChats).
const (
	DefaultEscalatePercent = 80
	DefaultAgentChatLimit  = 3
)

// handleAdmin renders the whole support settings page, optionally with one
// report alongside it.
func (s *Service) handleAdmin(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	ctx := r.Context()

	settings, err := s.Settings(ctx, id)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	policies, err := s.policyList(ctx)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	departments, err := s.Departments(ctx, id)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	categories, err := s.Categories(ctx)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	agents, err := s.Agents(ctx, id)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	canned, err := s.SharedCanned(ctx, id)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}

	data := adminData{
		Settings:    settings,
		Policies:    policies,
		Departments: departments,
		Categories:  categories,
		Agents:      agents,
		Canned:      canned,
		ReportNames: reportNames,
	}
	if name := strings.TrimSpace(r.URL.Query().Get("report")); name != "" {
		report, reportErr := s.Reports(ctx, id, name)
		if reportErr != nil {
			s.fail(w, r, id, reportErr)
			return
		}
		data.Report = report
		data.ReportName = name
	}

	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{
			"settings":         settings,
			"sla_policies":     policies,
			"departments":      departments,
			"categories":       categories,
			"agents":           agents,
			"canned_responses": canned,
		})
		return
	}
	s.render(w, r, http.StatusOK, "admin", s.newView(r, id, "Support settings", data))
}

// policyList returns the SLA tiers in priority order, so the page always shows
// them from most to least urgent rather than in map order.
func (s *Service) policyList(ctx context.Context) ([]SLAPolicy, error) {
	byPriority, err := s.SLAPolicies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SLAPolicy, 0, len(ticketPriorities))
	for _, priority := range ticketPriorities {
		if p, ok := byPriority[priority]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// handleAdminSettings saves one setting. The key must already be one the
// subsystem defines: an unknown key is rejected by the service layer rather
// than quietly stored.
func (s *Service) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	key := formValue(r, "key")
	if err := s.SetSetting(r.Context(), id, key, formValue(r, "value")); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{"key": key, "saved": true})
		return
	}
	s.redirect(w, r, "/admin")
}

// handleAdminSLA saves one SLA tier.
func (s *Service) handleAdminSLA(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	policy := SLAPolicy{
		ID:                formValue(r, "id"),
		Priority:          strings.ToUpper(strings.TrimSpace(formValue(r, "priority"))),
		FirstResponseMins: formInt(r, "first_response_mins", 0),
		ResolutionMins:    formInt(r, "resolution_mins", 0),
		EscalatePercent:   formInt(r, "escalate_percent", DefaultEscalatePercent),
		Enabled:           formBool(r, "enabled"),
	}
	saved, err := s.SaveSLAPolicy(r.Context(), id, policy)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, saved)
		return
	}
	s.redirect(w, r, "/admin")
}

// handleAdminDepartment creates or updates one department.
func (s *Service) handleAdminDepartment(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	saved, err := s.SaveDepartment(r.Context(), id, Department{
		ID:          formValue(r, "id"),
		Name:        formValue(r, "name"),
		Description: formValue(r, "description"),
		Enabled:     formBool(r, "enabled"),
	})
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, saved)
		return
	}
	s.redirect(w, r, "/admin")
}

// handleAdminCategory creates or updates one category. Retiring a category is
// done by disabling it, so the tickets already filed under it keep their
// history instead of losing it to a delete.
func (s *Service) handleAdminCategory(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	saved, err := s.SaveCategory(r.Context(), id, Category{
		ID:       formValue(r, "id"),
		ParentID: formValue(r, "parent_id"),
		Name:     formValue(r, "name"),
		Slug:     formValue(r, "slug"),
		Position: formInt(r, "position", 0),
		Enabled:  formBool(r, "enabled"),
	})
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, saved)
		return
	}
	s.redirect(w, r, "/admin")
}

// handleAdminAgent creates or updates one agent profile. The display name set
// here is the only name a customer ever sees on a reply.
func (s *Service) handleAdminAgent(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	userID, err := strconv.ParseInt(strings.TrimSpace(formValue(r, "user_id")), 10, 64)
	if err != nil {
		userID = 0
	}
	saved, saveErr := s.SaveAgent(r.Context(), id, Agent{
		ID:                 formValue(r, "id"),
		UserID:             userID,
		DisplayName:        formValue(r, "display_name"),
		DepartmentID:       formValue(r, "department_id"),
		MaxConcurrentChats: formInt(r, "max_concurrent_chats", DefaultAgentChatLimit),
		Enabled:            formBool(r, "enabled"),
	})
	if saveErr != nil {
		s.fail(w, r, id, saveErr)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, saved)
		return
	}
	s.redirect(w, r, "/admin")
}

// handleAdminCanned creates or updates a system or department response. The
// personal tier is not reachable here: an agent owns their own responses.
func (s *Service) handleAdminCanned(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	saved, err := s.SaveSharedCanned(r.Context(), id, formValue(r, "id"), cannedInputFrom(r))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, saved)
		return
	}
	s.redirect(w, r, "/admin")
}

// handleAdminReport returns one named report.
func (s *Service) handleAdminReport(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	name := r.PathValue("name")
	report, err := s.Reports(r.Context(), id, name)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, report)
		return
	}
	s.redirect(w, r, "/admin?report="+name)
}
