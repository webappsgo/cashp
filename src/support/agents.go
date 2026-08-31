package support

import (
	"context"
	"sort"
	"strings"

	"github.com/webappsgo/cashp/src/errors"
)

// requireAgent resolves the caller's agent record and insists they are working
// in support mode. Every agent-only action goes through here, so an account
// with the agent flag cannot act on other people's tickets simply by knowing a
// URL: they must have deliberately entered support mode first, and that entry
// is audit-logged with a reason.
func (s *Service) requireAgent(ctx context.Context, id Identity) (Agent, error) {
	if _, err := s.requireSupportMode(ctx, id); err != nil {
		return Agent{}, err
	}
	agent, err := s.store.AgentByUser(ctx, id.UserID)
	if err == nil {
		if !agent.Enabled {
			return Agent{}, errors.New(errors.CodeForbidden, 403, "This support account is disabled")
		}
		if err := s.store.TouchAgent(ctx, id.UserID, s.nowUnix()); err != nil {
			return Agent{}, err
		}
		agent.LastActivityAt = s.nowUnix()
		return agent, nil
	}
	if !id.GlobalAdmin {
		return Agent{}, err
	}

	// A global administrator is support staff by definition. Their agent
	// record is created on first use so they, too, appear to customers under a
	// plain display name rather than an administrative title.
	at := s.nowUnix()
	agent = Agent{
		ID:                 newID("agt"),
		UserID:             id.UserID,
		DisplayName:        firstNonEmpty(clean(id.DisplayName), "Support"),
		MaxConcurrentChats: 3,
		Enabled:            true,
		LastActivityAt:     at,
		CreatedAt:          at,
		UpdatedAt:          at,
	}
	if err := s.store.UpsertAgent(ctx, agent); err != nil {
		return Agent{}, err
	}
	return agent, nil
}

// firstNonEmpty returns the first value that is not empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// AvailabilityOf derives an agent's presence. It is never set by hand: it falls
// out of whether the agent is in support mode, how long they have been idle,
// and how many chats they are already carrying.
func (s *Service) AvailabilityOf(agent Agent, activeChats int, at int64) string {
	inMode := false
	s.modeMu.RLock()
	for _, m := range s.mode {
		if m.AgentUserID == agent.UserID && m.ExpiresAt > at {
			inMode = true
			break
		}
	}
	s.modeMu.RUnlock()

	if !agent.Enabled || !inMode {
		return AvailabilityOffline
	}
	if at-agent.LastActivityAt >= int64(AwayAfter.Seconds()) {
		return AvailabilityAway
	}
	limit := agent.MaxConcurrentChats
	if limit <= 0 {
		limit = 1
	}
	if activeChats >= limit {
		return AvailabilityBusy
	}
	return AvailabilityAvailable
}

// AgentPresence is one agent's derived standing, for the roster and for the
// chat availability check.
type AgentPresence struct {
	Agent       Agent
	Status      string
	ActiveChats int
}

// Roster returns every enabled agent with their derived availability.
func (s *Service) Roster(ctx context.Context) ([]AgentPresence, error) {
	agents, err := s.store.ListAgents(ctx, true)
	if err != nil {
		return nil, err
	}
	at := s.nowUnix()
	out := make([]AgentPresence, 0, len(agents))
	for _, a := range agents {
		active, err := s.store.CountActiveChatsForAgent(ctx, a.UserID)
		if err != nil {
			return nil, err
		}
		out = append(out, AgentPresence{
			Agent:       a,
			Status:      s.AvailabilityOf(a, active, at),
			ActiveChats: active,
		})
	}
	return out, nil
}

// QueueItem is a queued ticket together with its SLA standing.
type QueueItem struct {
	Ticket Ticket
	Risk   SLARisk
}

// Queue returns the tickets waiting for support attention, worst SLA standing
// first so the queue orders itself by real urgency rather than by age alone.
func (s *Service) Queue(ctx context.Context, id Identity, f TicketFilter) ([]QueueItem, Page, error) {
	if _, err := s.requireAgent(ctx, id); err != nil {
		return nil, Page{}, err
	}
	f.QueueOnly = true
	f.UserID = 0
	tickets, page, err := s.store.ListTickets(ctx, f)
	if err != nil {
		return nil, Page{}, err
	}
	policies, err := s.SLAPolicies(ctx)
	if err != nil {
		return nil, Page{}, err
	}

	at := s.nowUnix()
	items := make([]QueueItem, 0, len(tickets))
	for _, t := range tickets {
		policy, ok := policies[t.Priority]
		if !ok {
			policy = policies[PriorityNormal]
		}
		items = append(items, QueueItem{Ticket: t, Risk: EvaluateSLA(t, policy, at)})
	}
	sort.SliceStable(items, func(i, j int) bool {
		li, lj := riskRank(items[i].Risk.Level), riskRank(items[j].Risk.Level)
		if li != lj {
			return li > lj
		}
		pi, pj := PriorityRank(items[i].Ticket.Priority), PriorityRank(items[j].Ticket.Priority)
		if pi != pj {
			return pi > pj
		}
		return items[i].Ticket.CreatedAt < items[j].Ticket.CreatedAt
	})
	return items, page, nil
}

// riskRank orders the coarse SLA levels.
func riskRank(level string) int {
	switch level {
	case "breach":
		return 2
	case "warn":
		return 1
	default:
		return 0
	}
}

// AgentTicket loads a ticket for support staff, including internal notes. Staff
// work across tenants by design, so the lookup is not org-scoped — but it is
// reachable only from support mode and every read that leads to a change is
// recorded against the real actor.
func (s *Service) AgentTicket(ctx context.Context, id Identity, ticketID string) (Ticket, []TicketMessage, error) {
	if _, err := s.requireAgent(ctx, id); err != nil {
		return Ticket{}, nil, err
	}
	t, err := s.store.TicketAnyOrg(ctx, ticketID)
	if err != nil {
		return Ticket{}, nil, err
	}
	if t.Status == StateDraft {
		return Ticket{}, nil, notFound("Ticket")
	}
	msgs, err := s.store.Messages(ctx, t.OrgID, t.ID, true)
	if err != nil {
		return Ticket{}, nil, err
	}
	return t, msgs, nil
}

// ClaimTicket takes an unassigned ticket out of the queue.
func (s *Service) ClaimTicket(ctx context.Context, id Identity, ticketID string) (Ticket, error) {
	agent, err := s.requireAgent(ctx, id)
	if err != nil {
		return Ticket{}, err
	}
	t, err := s.store.TicketAnyOrg(ctx, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	if t.AssignedTo != 0 && t.AssignedTo != agent.UserID {
		return Ticket{}, errors.New(errors.CodeConflict, 409, "Another agent already has this ticket")
	}
	return s.assign(ctx, id, t, agent.UserID, "claimed from queue")
}

// AssignTicket hands a ticket to another agent.
func (s *Service) AssignTicket(ctx context.Context, id Identity, ticketID string, toUserID int64, reason string) (Ticket, error) {
	if _, err := s.requireAgent(ctx, id); err != nil {
		return Ticket{}, err
	}
	target, err := s.store.AgentByUser(ctx, toUserID)
	if err != nil {
		return Ticket{}, err
	}
	if !target.Enabled {
		return Ticket{}, errors.New(errors.CodeValidation, 400, "That support account is disabled")
	}
	t, err := s.store.TicketAnyOrg(ctx, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	return s.assign(ctx, id, t, target.UserID, firstNonEmpty(truncate(clean(reason), 200), "reassigned"))
}

// assign is the single write path for ownership changes. It records the change
// in the assignment log and moves an unclaimed ticket into ASSIGNED.
func (s *Service) assign(ctx context.Context, id Identity, t Ticket, toUserID int64, reason string) (Ticket, error) {
	from := t.AssignedTo
	at := s.nowUnix()

	t.AssignedTo = toUserID
	t.UpdatedAt = at
	if t.Status == StateOpen {
		if err := s.store.UpdateTicket(ctx, t); err != nil {
			return Ticket{}, err
		}
		t.Version++
		moved, err := s.applyTransition(ctx, id, t, StateAssigned, ActorAgent, reason)
		if err != nil {
			return Ticket{}, err
		}
		t = moved
	} else {
		if err := s.store.UpdateTicket(ctx, t); err != nil {
			return Ticket{}, err
		}
		t.Version++
	}

	if err := s.store.InsertAssignment(ctx, Assignment{
		ID:          newID("asg"),
		TicketID:    t.ID,
		OrgID:       t.OrgID,
		FromAgentID: from,
		ToAgentID:   toUserID,
		ActorID:     id.UserID,
		Reason:      reason,
		CreatedAt:   at,
	}); err != nil {
		return Ticket{}, err
	}
	if err := s.audit(ctx, id, AuditEntry{
		OrgID:      t.OrgID,
		Action:     "ticket.assign",
		EntityType: "ticket",
		EntityID:   t.ID,
		Detail:     reason,
	}); err != nil {
		return Ticket{}, err
	}
	s.notify(ctx, "support.ticket.assigned", toUserID, map[string]string{
		"ticket_number": t.Number,
		"title":         t.Title,
	})
	return t, nil
}

// AgentReply appends an agent message. An internal note stays inside support:
// it is filtered out of the customer's view in SQL, and it never moves the
// ticket's state, because the customer has not been told anything.
func (s *Service) AgentReply(ctx context.Context, id Identity, ticketID, body string, internal bool) (Ticket, error) {
	agent, err := s.requireAgent(ctx, id)
	if err != nil {
		return Ticket{}, err
	}
	t, err := s.store.TicketAnyOrg(ctx, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	if t.Status == StateDraft {
		return Ticket{}, notFound("Ticket")
	}
	body = truncate(cleanMultiline(body), 20000)
	if body == "" {
		return Ticket{}, errors.New(errors.CodeValidation, 400, "A reply cannot be empty").
			WithDetails(map[string]any{"field": "body"})
	}

	at := s.nowUnix()
	if err := s.store.InsertMessage(ctx, TicketMessage{
		ID:         newID("msg"),
		TicketID:   t.ID,
		OrgID:      t.OrgID,
		AuthorID:   agent.UserID,
		AuthorRole: ActorAgent,
		AuthorName: agent.DisplayName,
		Body:       body,
		Internal:   internal,
		CreatedAt:  at,
	}); err != nil {
		return Ticket{}, err
	}
	if internal {
		if err := s.audit(ctx, id, AuditEntry{
			OrgID:      t.OrgID,
			Action:     "ticket.note",
			EntityType: "ticket",
			EntityID:   t.ID,
		}); err != nil {
			return Ticket{}, err
		}
		return t, nil
	}

	if t.FirstResponseAt == 0 {
		t.FirstResponseAt = at
	}
	t.UpdatedAt = at
	if err := s.store.UpdateTicket(ctx, t); err != nil {
		return Ticket{}, err
	}
	t.Version++

	if CanTransition(t.Status, StateAwaitingUser, ActorAgent) {
		moved, err := s.applyTransition(ctx, id, t, StateAwaitingUser, ActorAgent, "agent replied")
		if err != nil {
			return Ticket{}, err
		}
		t = moved
	}
	s.notify(ctx, "support.ticket.reply", t.UserID, map[string]string{
		"ticket_number": t.Number,
	})
	return t, nil
}

// AgentTransition performs an explicit state change requested by an agent.
func (s *Service) AgentTransition(ctx context.Context, id Identity, ticketID, to, reason string) (Ticket, error) {
	if _, err := s.requireAgent(ctx, id); err != nil {
		return Ticket{}, err
	}
	to = strings.ToUpper(clean(to))
	if !IsState(to) {
		return Ticket{}, errors.New(errors.CodeValidation, 400, "Unknown ticket state").
			WithDetails(map[string]any{"field": "status"})
	}
	t, err := s.store.TicketAnyOrg(ctx, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	if t.Status == StateDraft {
		return Ticket{}, notFound("Ticket")
	}
	return s.applyTransition(ctx, id, t, to, ActorAgent, firstNonEmpty(truncate(clean(reason), 200), "agent action"))
}

// AgentMetrics is the summary an agent sees for their own work.
type AgentMetrics struct {
	Assigned            int
	Resolved            int
	QueueDepth          int
	AvgFirstResponseSec int64
	AvgResolutionSec    int64
}

// Metrics summarises the caller's own workload. It reads only tickets assigned
// to that agent plus the shared queue depth.
func (s *Service) Metrics(ctx context.Context, id Identity) (AgentMetrics, error) {
	agent, err := s.requireAgent(ctx, id)
	if err != nil {
		return AgentMetrics{}, err
	}
	mine, _, err := s.store.ListTickets(ctx, TicketFilter{
		AssignedTo: agent.UserID,
		Limit:      MaxPageLimit,
	})
	if err != nil {
		return AgentMetrics{}, err
	}
	depth, err := s.store.CountTickets(ctx, TicketFilter{QueueOnly: true})
	if err != nil {
		return AgentMetrics{}, err
	}

	m := AgentMetrics{QueueDepth: depth}
	var responseTotal, responseCount, resolveTotal, resolveCount int64
	for _, t := range mine {
		if IsOpenState(t.Status) {
			m.Assigned++
		}
		if t.ResolvedAt > 0 {
			m.Resolved++
			resolveTotal += t.ResolvedAt - t.CreatedAt
			resolveCount++
		}
		if t.FirstResponseAt > 0 {
			responseTotal += t.FirstResponseAt - t.CreatedAt
			responseCount++
		}
	}
	if responseCount > 0 {
		m.AvgFirstResponseSec = responseTotal / responseCount
	}
	if resolveCount > 0 {
		m.AvgResolutionSec = resolveTotal / resolveCount
	}
	return m, nil
}
