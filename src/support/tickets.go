package support

import (
	"context"
	"strconv"
	"strings"

	"github.com/webappsgo/cashp/src/errors"
)

// TicketInput is the set of fields a user may set on the ticket form. Every one
// of them is editable: the bot only proposes values, it never fixes them.
type TicketInput struct {
	Title       string
	Description string
	CategoryID  string
	Priority    string
}

// normalize cleans and bounds the submitted fields.
func (in TicketInput) normalize() TicketInput {
	in.Title = truncate(clean(in.Title), 200)
	in.Description = truncate(cleanMultiline(in.Description), 20000)
	in.CategoryID = truncate(clean(in.CategoryID), 64)
	in.Priority = strings.ToUpper(clean(in.Priority))
	if !IsPriority(in.Priority) {
		in.Priority = PriorityNormal
	}
	return in
}

// validate rejects a submission that cannot become a ticket.
func (in TicketInput) validate() error {
	if in.Title == "" {
		return errors.New(errors.CodeValidation, 400, "A short summary is required").
			WithDetails(map[string]any{"field": "title"})
	}
	if in.Description == "" {
		return errors.New(errors.CodeValidation, 400, "A description is required").
			WithDetails(map[string]any{"field": "description"})
	}
	return nil
}

// requireTicketCreator is the guard on every path that creates a ticket. It
// enforces two rules at once: guests cannot open tickets, and a member of staff
// who is in support mode cannot open one either — they are acting as support at
// that moment, not as a customer.
func (s *Service) requireTicketCreator(ctx context.Context, id Identity) error {
	if !id.Authenticated || id.UserID == 0 || id.OrgID == 0 {
		return errors.New(errors.CodeUnauthorized, 401, "Sign in to open a support ticket")
	}
	if _, inMode := s.SupportModeFor(id); inMode {
		return errors.New(errors.CodeForbidden, 403,
			"Exit support mode before opening a ticket of your own")
	}
	return nil
}

// StartBotSession opens a bot conversation. Every ticket begins here: there is
// no route that creates a ticket without a bot session behind it.
func (s *Service) StartBotSession(ctx context.Context, id Identity) (BotSession, error) {
	if err := s.requireTicketCreator(ctx, id); err != nil {
		return BotSession{}, err
	}
	at := s.nowUnix()
	session := BotSession{
		ID:           newID("bot"),
		OrgID:        id.OrgID,
		UserID:       id.UserID,
		LastPriority: PriorityNormal,
		CreatedAt:    at,
		UpdatedAt:    at,
	}
	if err := s.store.InsertBotSession(ctx, session); err != nil {
		return BotSession{}, err
	}
	return session, nil
}

// BotGreeting is the bot's opening line.
const BotGreeting = "Hi! I can help. What's the issue?"

// BotMessage advances a bot conversation. It never creates or modifies a
// ticket: the only thing it can produce is a suggestion for the ticket form.
func (s *Service) BotMessage(ctx context.Context, id Identity, sessionID, text string) (BotSession, BotReply, error) {
	if err := s.requireTicketCreator(ctx, id); err != nil {
		return BotSession{}, BotReply{}, err
	}
	if err := s.allow(limitBotMessage, id); err != nil {
		return BotSession{}, BotReply{}, err
	}
	session, err := s.store.BotSession(ctx, id.UserID, sessionID)
	if err != nil {
		return BotSession{}, BotReply{}, err
	}
	if session.OrgID != id.OrgID {
		return BotSession{}, BotReply{}, notFound("Help session")
	}

	session, reply := AskBot(session, text)
	session.UpdatedAt = s.nowUnix()
	if err := s.store.UpdateBotSession(ctx, session); err != nil {
		return BotSession{}, BotReply{}, err
	}
	return session, reply, nil
}

// MarkBotResolved records that the bot's answer solved the problem. No ticket
// is created and the conversation ends.
func (s *Service) MarkBotResolved(ctx context.Context, id Identity, sessionID string) error {
	session, err := s.store.BotSession(ctx, id.UserID, sessionID)
	if err != nil {
		return err
	}
	if session.OrgID != id.OrgID {
		return notFound("Help session")
	}
	session.Resolved = true
	session.UpdatedAt = s.nowUnix()
	return s.store.UpdateBotSession(ctx, session)
}

// EscalateBotSession hands a bot conversation to the ticket form and creates
// the server-side DRAFT the form autosaves into. The draft is not a ticket: it
// is invisible to agents and becomes one only when the user submits it.
func (s *Service) EscalateBotSession(ctx context.Context, id Identity, sessionID string) (Ticket, TicketPrefill, error) {
	if err := s.requireTicketCreator(ctx, id); err != nil {
		return Ticket{}, TicketPrefill{}, err
	}
	session, err := s.store.BotSession(ctx, id.UserID, sessionID)
	if err != nil {
		return Ticket{}, TicketPrefill{}, err
	}
	if session.OrgID != id.OrgID {
		return Ticket{}, TicketPrefill{}, notFound("Help session")
	}

	turns := TranscriptLines(session.Transcript)
	lastUser := ""
	for _, turn := range turns {
		if turn.Speaker == "user" {
			lastUser = turn.Text
		}
	}
	prefill := prefillFrom(lastUser, session.LastCategory, session.LastPriority)

	number, err := s.store.NextTicketNumber(ctx)
	if err != nil {
		return Ticket{}, TicketPrefill{}, err
	}
	at := s.nowUnix()
	draft := Ticket{
		ID:          newID("tkt"),
		OrgID:       id.OrgID,
		Number:      number,
		Title:       prefill.Title,
		Description: prefill.Description,
		CategoryID:  prefill.Category,
		Priority:    prefill.Priority,
		Status:      StateDraft,
		UserID:      id.UserID,
		BotContext:  session.Transcript,
		CreatedAt:   at,
		UpdatedAt:   at,
		Version:     1,
	}
	if err := s.store.InsertTicket(ctx, draft); err != nil {
		return Ticket{}, TicketPrefill{}, err
	}

	session.Escalated = true
	session.UpdatedAt = at
	if err := s.store.UpdateBotSession(ctx, session); err != nil {
		return Ticket{}, TicketPrefill{}, err
	}
	if err := s.audit(ctx, id, AuditEntry{
		Action:     "ticket.draft",
		EntityType: "ticket",
		EntityID:   draft.ID,
		ToState:    StateDraft,
	}); err != nil {
		return Ticket{}, TicketPrefill{}, err
	}
	return draft, prefill, nil
}

// SaveDraft stores the ticket form's autosave. It only ever touches the
// caller's own DRAFT, and it never changes the ticket's state.
func (s *Service) SaveDraft(ctx context.Context, id Identity, ticketID string, in TicketInput) (Ticket, error) {
	if err := s.requireTicketCreator(ctx, id); err != nil {
		return Ticket{}, err
	}
	t, err := s.store.Ticket(ctx, id.OrgID, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	if t.UserID != id.UserID || t.Status != StateDraft {
		return Ticket{}, notFound("Draft")
	}

	in = in.normalize()
	t.Title = in.Title
	t.Description = in.Description
	t.CategoryID = in.CategoryID
	t.Priority = in.Priority
	t.UpdatedAt = s.nowUnix()
	if err := s.store.UpdateTicket(ctx, t); err != nil {
		return Ticket{}, err
	}
	t.Version++
	return t, nil
}

// SubmitTicket moves the caller's draft to OPEN. This is the only path that
// opens a ticket, and it runs only on an explicit user action.
func (s *Service) SubmitTicket(ctx context.Context, id Identity, ticketID string, in TicketInput) (Ticket, error) {
	if err := s.requireTicketCreator(ctx, id); err != nil {
		return Ticket{}, err
	}
	if err := s.allow(limitTicketCreate, id); err != nil {
		return Ticket{}, err
	}

	t, err := s.store.Ticket(ctx, id.OrgID, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	if t.UserID != id.UserID {
		return Ticket{}, notFound("Draft")
	}
	if !CanTransition(t.Status, StateOpen, ActorUser) {
		return Ticket{}, &TransitionError{From: t.Status, To: StateOpen, Actor: ActorUser}
	}

	in = in.normalize()
	if err := in.validate(); err != nil {
		return Ticket{}, err
	}

	policy, err := s.PolicyFor(ctx, Ticket{Priority: in.Priority})
	if err != nil {
		return Ticket{}, err
	}

	at := s.nowUnix()
	t.Title = in.Title
	t.Description = in.Description
	t.CategoryID = in.CategoryID
	t.Priority = in.Priority
	t.Status = StateOpen
	t.SLAPolicyID = policy.ID
	// The SLA clock starts when the ticket enters the queue, not when the
	// draft was created, so the created stamp is reset on submission.
	t.CreatedAt = at
	t.UpdatedAt = at
	if err := s.store.UpdateTicket(ctx, t); err != nil {
		return Ticket{}, err
	}
	t.Version++

	if err := s.audit(ctx, id, AuditEntry{
		Action:     "ticket.submit",
		EntityType: "ticket",
		EntityID:   t.ID,
		FromState:  StateDraft,
		ToState:    StateOpen,
	}); err != nil {
		return Ticket{}, err
	}
	s.notify(ctx, "support.ticket.created", t.UserID, map[string]string{
		"ticket_number": t.Number,
		"title":         t.Title,
	})
	return t, nil
}

// Ticket loads a ticket for a tenant user together with the conversation the
// user is allowed to see. Internal notes are excluded in SQL.
func (s *Service) Ticket(ctx context.Context, id Identity, ticketID string) (Ticket, []TicketMessage, error) {
	if !id.Authenticated || id.OrgID == 0 {
		return Ticket{}, nil, errors.New(errors.CodeUnauthorized, 401, "Authentication required")
	}
	t, err := s.store.Ticket(ctx, id.OrgID, ticketID)
	if err != nil {
		return Ticket{}, nil, err
	}
	if t.UserID != id.UserID && !id.OrgAdmin {
		return Ticket{}, nil, notFound("Ticket")
	}
	msgs, err := s.store.Messages(ctx, id.OrgID, t.ID, false)
	if err != nil {
		return Ticket{}, nil, err
	}
	return t, msgs, nil
}

// ListTickets lists the caller's tickets. An organization administrator sees
// every ticket raised inside their own organization and nothing beyond it.
func (s *Service) ListTickets(ctx context.Context, id Identity, f TicketFilter) ([]Ticket, Page, error) {
	if !id.Authenticated || id.OrgID == 0 {
		return nil, Page{}, errors.New(errors.CodeUnauthorized, 401, "Authentication required")
	}
	f.OrgID = id.OrgID
	if !id.OrgAdmin {
		f.UserID = id.UserID
	}
	f.AssignedTo = 0
	return s.store.ListTickets(ctx, f)
}

// AddUserReply appends a tenant reply and advances the conversation state.
func (s *Service) AddUserReply(ctx context.Context, id Identity, ticketID, body string) (Ticket, error) {
	t, _, err := s.Ticket(ctx, id, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	body = truncate(cleanMultiline(body), 20000)
	if body == "" {
		return Ticket{}, errors.New(errors.CodeValidation, 400, "A reply cannot be empty").
			WithDetails(map[string]any{"field": "body"})
	}
	if !IsOpenState(t.Status) && t.Status != StateResolved {
		return Ticket{}, errors.New(errors.CodeConflict, 409, "This ticket is closed")
	}

	at := s.nowUnix()
	if err := s.store.InsertMessage(ctx, TicketMessage{
		ID:         newID("msg"),
		TicketID:   t.ID,
		OrgID:      t.OrgID,
		AuthorID:   id.UserID,
		AuthorRole: ActorUser,
		AuthorName: id.DisplayName,
		Body:       body,
		CreatedAt:  at,
	}); err != nil {
		return Ticket{}, err
	}

	if CanTransition(t.Status, StateAwaitingAgent, ActorUser) {
		return s.applyTransition(ctx, id, t, StateAwaitingAgent, ActorUser, "user replied")
	}
	t.UpdatedAt = at
	if err := s.store.UpdateTicket(ctx, t); err != nil {
		return Ticket{}, err
	}
	t.Version++
	if t.AssignedTo != 0 {
		s.notify(ctx, "support.ticket.reply", t.AssignedTo, map[string]string{
			"ticket_number": t.Number,
		})
	}
	return t, nil
}

// CloseTicket lets the user accept a resolution and close their own ticket.
func (s *Service) CloseTicket(ctx context.Context, id Identity, ticketID string) (Ticket, error) {
	t, _, err := s.Ticket(ctx, id, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	return s.applyTransition(ctx, id, t, StateClosed, ActorUser, "closed by user")
}

// ReopenTicket lets the user reopen a closed ticket. The ticket passes through
// REOPENED and the system immediately drains it back to OPEN.
func (s *Service) ReopenTicket(ctx context.Context, id Identity, ticketID string) (Ticket, error) {
	t, _, err := s.Ticket(ctx, id, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	reopened, err := s.applyTransition(ctx, id, t, StateReopened, ActorUser, "reopened by user")
	if err != nil {
		return Ticket{}, err
	}
	drained, err := s.applyTransition(ctx, id, reopened, StateOpen, ActorSystem, "requeued after reopen")
	if err != nil {
		return Ticket{}, err
	}
	if drained.AssignedTo != 0 {
		s.notify(ctx, "support.ticket.reopened", drained.AssignedTo, map[string]string{
			"ticket_number": drained.Number,
		})
	}
	return drained, nil
}

// applyTransition is the single write path for a state change. Nothing in this
// package changes ticket.Status without going through it, so the transition
// table and the audit log can never disagree with the stored state.
func (s *Service) applyTransition(ctx context.Context, id Identity, t Ticket, to, actor, reason string) (Ticket, error) {
	if !CanTransition(t.Status, to, actor) {
		return Ticket{}, &TransitionError{From: t.Status, To: to, Actor: actor}
	}

	at := s.nowUnix()
	from := t.Status
	t.Status = to
	t.UpdatedAt = at

	switch to {
	case StateResolved:
		t.ResolvedAt = at
	case StateClosed:
		t.ClosedAt = at
	case StateOpen:
		if from == StateReopened {
			t.ResolvedAt = 0
			t.ClosedAt = 0
			t.AssignedTo = 0
			t.CreatedAt = at
			t.FirstResponseAt = 0
		}
	}

	if err := s.store.UpdateTicket(ctx, t); err != nil {
		return Ticket{}, err
	}
	t.Version++

	if err := s.audit(ctx, id, AuditEntry{
		OrgID:      t.OrgID,
		Action:     "ticket.transition",
		EntityType: "ticket",
		EntityID:   t.ID,
		FromState:  from,
		ToState:    to,
		Detail:     reason,
	}); err != nil {
		return Ticket{}, err
	}

	switch to {
	case StateResolved:
		s.notify(ctx, "support.ticket.resolved", t.UserID, map[string]string{
			"ticket_number": t.Number,
		})
	case StateClosed:
		s.notify(ctx, "support.ticket.closed", t.UserID, map[string]string{
			"ticket_number": t.Number,
		})
	}
	return t, nil
}

// TicketAudit returns a ticket's audit trail for the tenant that owns it.
func (s *Service) TicketAudit(ctx context.Context, id Identity, ticketID string) ([]AuditEntry, error) {
	t, _, err := s.Ticket(ctx, id, ticketID)
	if err != nil {
		return nil, err
	}
	return s.store.AuditFor(ctx, "ticket", t.ID)
}

// ticketOwnerKey renders a ticket owner id for an audit detail field.
func ticketOwnerKey(userID int64) string {
	return strconv.FormatInt(userID, 10)
}
