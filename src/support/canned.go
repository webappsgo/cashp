package support

import (
	"context"
	"sort"
	"strings"

	"github.com/webappsgo/cashp/src/errors"
)

// CannedInput is the editable part of a canned response.
type CannedInput struct {
	Title        string
	Body         string
	Tags         string
	Scope        string
	DepartmentID string
}

// normalize cleans and bounds the submitted fields.
func (in CannedInput) normalize() CannedInput {
	in.Title = truncate(clean(in.Title), 120)
	in.Body = truncate(cleanMultiline(in.Body), 8000)
	in.Tags = truncate(strings.ToLower(clean(in.Tags)), 300)
	in.Scope = strings.ToUpper(clean(in.Scope))
	in.DepartmentID = truncate(clean(in.DepartmentID), 64)
	return in
}

// validate rejects an unusable canned response.
func (in CannedInput) validate() error {
	if in.Title == "" {
		return errors.New(errors.CodeValidation, 400, "A title is required").
			WithDetails(map[string]any{"field": "title"})
	}
	if in.Body == "" {
		return errors.New(errors.CodeValidation, 400, "A body is required").
			WithDetails(map[string]any{"field": "body"})
	}
	switch in.Scope {
	case CannedSystem, CannedDepartment, CannedPersonal:
	default:
		return errors.New(errors.CodeValidation, 400, "Unknown canned response scope").
			WithDetails(map[string]any{"field": "scope"})
	}
	if in.Scope == CannedDepartment && in.DepartmentID == "" {
		return errors.New(errors.CodeValidation, 400, "A department response needs a department").
			WithDetails(map[string]any{"field": "department_id"})
	}
	return nil
}

// CannedResponses returns everything the calling agent may use: every SYSTEM
// response, the responses for their own department, and their own PERSONAL
// ones. Another agent's personal responses are never included.
func (s *Service) CannedResponses(ctx context.Context, id Identity) ([]CannedResponse, error) {
	agent, err := s.requireAgent(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.store.ListCannedFor(ctx, agent.UserID, agent.DepartmentID)
}

// CreatePersonalCanned saves a response for the calling agent alone. An agent
// may only ever create at PERSONAL scope; SYSTEM and DEPARTMENT responses are
// an administrator's to write.
func (s *Service) CreatePersonalCanned(ctx context.Context, id Identity, in CannedInput) (CannedResponse, error) {
	agent, err := s.requireAgent(ctx, id)
	if err != nil {
		return CannedResponse{}, err
	}
	in = in.normalize()
	in.Scope = CannedPersonal
	in.DepartmentID = ""
	if err := in.validate(); err != nil {
		return CannedResponse{}, err
	}

	at := s.nowUnix()
	c := CannedResponse{
		ID:          newID("can"),
		Scope:       CannedPersonal,
		AgentUserID: agent.UserID,
		Title:       in.Title,
		Body:        in.Body,
		Tags:        in.Tags,
		CreatedAt:   at,
		UpdatedAt:   at,
	}
	if err := s.store.InsertCanned(ctx, c); err != nil {
		return CannedResponse{}, err
	}
	return c, nil
}

// UpdatePersonalCanned edits one of the calling agent's own responses.
func (s *Service) UpdatePersonalCanned(ctx context.Context, id Identity, cannedID string, in CannedInput) (CannedResponse, error) {
	agent, err := s.requireAgent(ctx, id)
	if err != nil {
		return CannedResponse{}, err
	}
	c, err := s.store.Canned(ctx, cannedID)
	if err != nil {
		return CannedResponse{}, err
	}
	if c.Scope != CannedPersonal || c.AgentUserID != agent.UserID {
		return CannedResponse{}, notFound("Canned response")
	}

	in = in.normalize()
	in.Scope = CannedPersonal
	if err := in.validate(); err != nil {
		return CannedResponse{}, err
	}
	c.Title = in.Title
	c.Body = in.Body
	c.Tags = in.Tags
	c.UpdatedAt = s.nowUnix()
	if err := s.store.UpdateCanned(ctx, c); err != nil {
		return CannedResponse{}, err
	}
	return c, nil
}

// DeletePersonalCanned removes one of the calling agent's own responses.
func (s *Service) DeletePersonalCanned(ctx context.Context, id Identity, cannedID string) error {
	agent, err := s.requireAgent(ctx, id)
	if err != nil {
		return err
	}
	return s.store.DeleteCanned(ctx, cannedID, agent.UserID)
}

// UseCanned returns a response's body and records the use. Visibility is
// checked before anything is returned, so an agent cannot read a colleague's
// personal response by guessing its identifier.
func (s *Service) UseCanned(ctx context.Context, id Identity, cannedID string) (CannedResponse, error) {
	visible, err := s.CannedResponses(ctx, id)
	if err != nil {
		return CannedResponse{}, err
	}
	for _, c := range visible {
		if c.ID != cannedID {
			continue
		}
		if err := s.store.BumpCannedUsage(ctx, c.ID); err != nil {
			return CannedResponse{}, err
		}
		c.UsageCount++
		return c, nil
	}
	return CannedResponse{}, notFound("Canned response")
}

// CannedSuggestion is one suggested response with the score that ranked it.
type CannedSuggestion struct {
	Response CannedResponse
	Score    int
}

// SuggestCanned ranks the agent's visible responses against the conversation in
// front of them. The score is the number of a response's tags that appear in
// the text, so the same conversation always suggests the same responses.
func (s *Service) SuggestCanned(ctx context.Context, id Identity, text string, max int) ([]CannedSuggestion, error) {
	visible, err := s.CannedResponses(ctx, id)
	if err != nil {
		return nil, err
	}
	if max <= 0 {
		return nil, nil
	}
	haystack := " " + strings.ToLower(cleanMultiline(text)) + " "

	var out []CannedSuggestion
	for _, c := range visible {
		score := 0
		for _, tag := range strings.Split(c.Tags, ",") {
			tag = strings.TrimSpace(strings.ToLower(tag))
			if len(tag) < 3 {
				continue
			}
			if strings.Contains(haystack, tag) {
				score++
			}
		}
		if score == 0 {
			continue
		}
		out = append(out, CannedSuggestion{Response: c, Score: score})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Response.UsageCount != out[j].Response.UsageCount {
			return out[i].Response.UsageCount > out[j].Response.UsageCount
		}
		return out[i].Response.ID < out[j].Response.ID
	})
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}
