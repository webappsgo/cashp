package support

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/errors"
)

// ChatAvailability is the answer to "can I start a chat right now?". When any
// part of it is false the widget says so plainly and offers a ticket instead of
// showing a button that cannot work.
type ChatAvailability struct {
	Available     bool
	Reason        string
	QueueLength   int
	AgentsOnline  int
	Capacity      int
	ActiveChats   int
	EstimatedWait time.Duration
}

// ChatAvailable evaluates the four conditions that must all hold: the feature
// is switched on, the clock is inside business hours, at least one agent is
// available, and the installation is below its concurrent chat ceiling.
func (s *Service) ChatAvailable(ctx context.Context) (ChatAvailability, error) {
	out := ChatAvailability{}
	if !s.settingBool(ctx, SettingChatEnabled) {
		out.Reason = "Live chat is turned off. Open a ticket and we will reply by email."
		return out, nil
	}
	if !s.withinBusinessHours(ctx, s.opts.Now().UTC()) {
		out.Reason = "Live chat is outside its hours right now. Open a ticket and we will reply by email."
		return out, nil
	}

	roster, err := s.Roster(ctx)
	if err != nil {
		return ChatAvailability{}, err
	}
	for _, p := range roster {
		if p.Status == AvailabilityAvailable {
			out.AgentsOnline++
		}
		out.ActiveChats += p.ActiveChats
	}

	queued, err := s.store.ChatSessionsByStatus(ctx, ChatQueued)
	if err != nil {
		return ChatAvailability{}, err
	}
	out.QueueLength = len(queued)

	out.Capacity = s.settingInt(ctx, SettingChatMaxConcurrent)
	if out.Capacity <= 0 {
		out.Capacity = 10
	}
	if out.AgentsOnline == 0 {
		out.Reason = "No one is on chat at the moment. Open a ticket and we will reply by email."
		return out, nil
	}
	if out.ActiveChats+out.QueueLength >= out.Capacity {
		out.Reason = "Chat is at capacity right now. Open a ticket and we will reply by email."
		return out, nil
	}

	out.Available = true
	// Six minutes per waiting conversation, divided across the agents who are
	// actually free. It is an estimate and is labelled as one in the UI.
	out.EstimatedWait = time.Duration(out.QueueLength) * 6 * time.Minute / time.Duration(out.AgentsOnline)
	return out, nil
}

// withinBusinessHours reports whether the given instant is inside the
// configured chat window. The window is stored in the database in UTC minutes
// past midnight and is edited from the admin panel.
func (s *Service) withinBusinessHours(ctx context.Context, at time.Time) bool {
	days := s.settingString(ctx, SettingChatDays)
	if strings.TrimSpace(days) == "" {
		return false
	}
	weekday := int(at.Weekday())
	allowed := false
	for _, field := range strings.Split(days, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err == nil && n == weekday {
			allowed = true
			break
		}
	}
	if !allowed {
		return false
	}

	open := s.settingInt(ctx, SettingChatOpenMinute)
	closed := s.settingInt(ctx, SettingChatCloseMinute)
	minute := at.Hour()*60 + at.Minute()
	if open == closed {
		return true
	}
	if open < closed {
		return minute >= open && minute < closed
	}
	// A window that wraps past midnight.
	return minute >= open || minute < closed
}

// StartChat puts a customer into the chat queue.
func (s *Service) StartChat(ctx context.Context, id Identity, subject string) (ChatSession, error) {
	if !id.Authenticated || id.UserID == 0 || id.OrgID == 0 {
		return ChatSession{}, errors.New(errors.CodeUnauthorized, 401, "Sign in to start a chat")
	}
	if _, inMode := s.SupportModeFor(id); inMode {
		return ChatSession{}, errors.New(errors.CodeForbidden, 403,
			"Exit support mode before starting a chat of your own")
	}
	if err := s.allow(limitChatStart, id); err != nil {
		return ChatSession{}, err
	}
	availability, err := s.ChatAvailable(ctx)
	if err != nil {
		return ChatSession{}, err
	}
	if !availability.Available {
		return ChatSession{}, errors.New(errors.CodeUnavailable, 503, availability.Reason)
	}

	at := s.nowUnix()
	session := ChatSession{
		ID:          newID("cht"),
		OrgID:       id.OrgID,
		UserID:      id.UserID,
		Status:      ChatQueued,
		Subject:     truncate(clean(subject), 200),
		QueuedAt:    at,
		LastEventAt: at,
	}
	if err := s.store.InsertChatSession(ctx, session); err != nil {
		return ChatSession{}, err
	}
	if err := s.audit(ctx, id, AuditEntry{
		Action:     "chat.start",
		EntityType: "chat",
		EntityID:   session.ID,
		ToState:    ChatQueued,
	}); err != nil {
		return ChatSession{}, err
	}
	return session, nil
}

// ChatView returns a session and the messages after a given timestamp, for the
// customer who owns it. Passing after=0 returns the whole conversation, which
// is what a plain page load without JavaScript does.
func (s *Service) ChatView(ctx context.Context, id Identity, sessionID string, after int64) (ChatSession, []ChatMessage, error) {
	if !id.Authenticated || id.OrgID == 0 {
		return ChatSession{}, nil, errors.New(errors.CodeUnauthorized, 401, "Authentication required")
	}
	session, err := s.store.ChatSession(ctx, id.OrgID, sessionID)
	if err != nil {
		return ChatSession{}, nil, err
	}
	if session.UserID != id.UserID {
		return ChatSession{}, nil, notFound("Chat")
	}
	msgs, err := s.store.ChatMessages(ctx, session.OrgID, session.ID, after)
	if err != nil {
		return ChatSession{}, nil, err
	}
	return session, msgs, nil
}

// QueuePosition reports how many conversations are ahead of one session.
func (s *Service) QueuePosition(ctx context.Context, session ChatSession) (int, error) {
	if session.Status != ChatQueued {
		return 0, nil
	}
	queued, err := s.store.ChatSessionsByStatus(ctx, ChatQueued)
	if err != nil {
		return 0, err
	}
	position := 1
	for _, q := range queued {
		if q.QueuedAt < session.QueuedAt {
			position++
		}
	}
	return position, nil
}

// PostChatMessage appends a customer message to their own chat.
func (s *Service) PostChatMessage(ctx context.Context, id Identity, sessionID, body string) (ChatMessage, error) {
	session, _, err := s.ChatView(ctx, id, sessionID, 0)
	if err != nil {
		return ChatMessage{}, err
	}
	if session.Status != ChatActive && session.Status != ChatQueued {
		return ChatMessage{}, errors.New(errors.CodeConflict, 409, "This chat has ended")
	}
	return s.appendChatMessage(ctx, session, id.UserID, ActorUser, id.DisplayName, body)
}

// appendChatMessage is the single write path for chat messages.
func (s *Service) appendChatMessage(ctx context.Context, session ChatSession, authorID int64, role, name, body string) (ChatMessage, error) {
	body = truncate(cleanMultiline(body), 8000)
	if body == "" {
		return ChatMessage{}, errors.New(errors.CodeValidation, 400, "A message cannot be empty").
			WithDetails(map[string]any{"field": "body"})
	}
	at := s.nowUnix()
	m := ChatMessage{
		ID:         newID("cms"),
		SessionID:  session.ID,
		OrgID:      session.OrgID,
		AuthorID:   authorID,
		AuthorRole: role,
		AuthorName: firstNonEmpty(clean(name), "Support"),
		Body:       body,
		CreatedAt:  at,
	}
	if err := s.store.InsertChatMessage(ctx, m); err != nil {
		return ChatMessage{}, err
	}
	session.LastEventAt = at
	if err := s.store.UpdateChatSession(ctx, session); err != nil {
		return ChatMessage{}, err
	}
	return m, nil
}

// AgentChats lists the conversations waiting for, or being handled by, support.
func (s *Service) AgentChats(ctx context.Context, id Identity) ([]ChatSession, error) {
	if _, err := s.requireAgent(ctx, id); err != nil {
		return nil, err
	}
	return s.store.ChatSessionsByStatus(ctx, ChatQueued, ChatActive)
}

// AcceptChat takes a queued conversation. The first agent to accept wins; a
// second attempt is refused rather than silently reassigning the customer.
func (s *Service) AcceptChat(ctx context.Context, id Identity, sessionID string) (ChatSession, error) {
	agent, err := s.requireAgent(ctx, id)
	if err != nil {
		return ChatSession{}, err
	}
	session, err := s.store.ChatSessionAnyOrg(ctx, sessionID)
	if err != nil {
		return ChatSession{}, err
	}
	if session.Status != ChatQueued {
		return ChatSession{}, errors.New(errors.CodeConflict, 409, "That chat is no longer waiting")
	}

	active, err := s.store.CountActiveChatsForAgent(ctx, agent.UserID)
	if err != nil {
		return ChatSession{}, err
	}
	limit := agent.MaxConcurrentChats
	if limit <= 0 {
		limit = 1
	}
	if active >= limit {
		return ChatSession{}, errors.New(errors.CodeConflict, 409, "You are already at your chat limit")
	}

	at := s.nowUnix()
	session.Status = ChatActive
	session.AgentID = agent.UserID
	session.StartedAt = at
	session.LastEventAt = at
	if err := s.store.UpdateChatSession(ctx, session); err != nil {
		return ChatSession{}, err
	}
	if err := s.audit(ctx, id, AuditEntry{
		OrgID:      session.OrgID,
		Action:     "chat.accept",
		EntityType: "chat",
		EntityID:   session.ID,
		FromState:  ChatQueued,
		ToState:    ChatActive,
	}); err != nil {
		return ChatSession{}, err
	}
	s.notify(ctx, "support.chat.started", session.UserID, map[string]string{
		"agent": agent.DisplayName,
	})
	return session, nil
}

// AgentChatView returns a session and its messages for the agent handling it.
func (s *Service) AgentChatView(ctx context.Context, id Identity, sessionID string, after int64) (ChatSession, []ChatMessage, error) {
	if _, err := s.requireAgent(ctx, id); err != nil {
		return ChatSession{}, nil, err
	}
	session, err := s.store.ChatSessionAnyOrg(ctx, sessionID)
	if err != nil {
		return ChatSession{}, nil, err
	}
	msgs, err := s.store.ChatMessages(ctx, session.OrgID, session.ID, after)
	if err != nil {
		return ChatSession{}, nil, err
	}
	return session, msgs, nil
}

// AgentChatMessage appends an agent message. The byline is the agent's display
// name: the customer is never shown a role or a hierarchy label.
func (s *Service) AgentChatMessage(ctx context.Context, id Identity, sessionID, body string) (ChatMessage, error) {
	agent, err := s.requireAgent(ctx, id)
	if err != nil {
		return ChatMessage{}, err
	}
	session, err := s.store.ChatSessionAnyOrg(ctx, sessionID)
	if err != nil {
		return ChatMessage{}, err
	}
	if session.Status != ChatActive {
		return ChatMessage{}, errors.New(errors.CodeConflict, 409, "This chat is not active")
	}
	if session.AgentID != agent.UserID {
		return ChatMessage{}, errors.New(errors.CodeForbidden, 403, "Another agent is handling this chat")
	}
	return s.appendChatMessage(ctx, session, agent.UserID, ActorAgent, agent.DisplayName, body)
}

// CloseChat ends a conversation. Either side may end it; a rating is optional
// and is accepted only from the customer.
func (s *Service) CloseChat(ctx context.Context, id Identity, sessionID string, rating int) (ChatSession, error) {
	session, err := s.chatForParticipant(ctx, id, sessionID)
	if err != nil {
		return ChatSession{}, err
	}
	if session.Status == ChatClosed || session.Status == ChatEscalated {
		return session, nil
	}

	from := session.Status
	at := s.nowUnix()
	if session.Status == ChatQueued {
		session.Status = ChatAbandoned
	} else {
		session.Status = ChatClosed
	}
	session.EndedAt = at
	session.LastEventAt = at
	if session.UserID == id.UserID && rating >= 1 && rating <= 5 {
		session.Rating = rating
	}
	if err := s.store.UpdateChatSession(ctx, session); err != nil {
		return ChatSession{}, err
	}
	if err := s.audit(ctx, id, AuditEntry{
		OrgID:      session.OrgID,
		Action:     "chat.close",
		EntityType: "chat",
		EntityID:   session.ID,
		FromState:  from,
		ToState:    session.Status,
	}); err != nil {
		return ChatSession{}, err
	}
	return session, nil
}

// chatForParticipant resolves a session for whichever side of it is asking.
func (s *Service) chatForParticipant(ctx context.Context, id Identity, sessionID string) (ChatSession, error) {
	if _, inMode := s.SupportModeFor(id); inMode {
		if _, err := s.requireAgent(ctx, id); err != nil {
			return ChatSession{}, err
		}
		return s.store.ChatSessionAnyOrg(ctx, sessionID)
	}
	session, _, err := s.ChatView(ctx, id, sessionID, 0)
	return session, err
}

// EscalateChat turns a conversation into a ticket. The transcript becomes the
// ticket's opening description so the customer does not have to repeat
// themselves, and the ticket belongs to the customer, not to the agent.
func (s *Service) EscalateChat(ctx context.Context, id Identity, sessionID, title, priority string) (Ticket, error) {
	agent, err := s.requireAgent(ctx, id)
	if err != nil {
		return Ticket{}, err
	}
	session, err := s.store.ChatSessionAnyOrg(ctx, sessionID)
	if err != nil {
		return Ticket{}, err
	}
	if session.Status != ChatActive {
		return Ticket{}, errors.New(errors.CodeConflict, 409, "Only an active chat can become a ticket")
	}
	if session.AgentID != agent.UserID {
		return Ticket{}, errors.New(errors.CodeForbidden, 403, "Another agent is handling this chat")
	}

	msgs, err := s.store.ChatMessages(ctx, session.OrgID, session.ID, 0)
	if err != nil {
		return Ticket{}, err
	}
	var transcript strings.Builder
	transcript.WriteString("Chat transcript\n")
	for _, m := range msgs {
		transcript.WriteString(m.AuthorName)
		transcript.WriteString(": ")
		transcript.WriteString(strings.ReplaceAll(m.Body, "\n", " "))
		transcript.WriteString("\n")
	}

	priority = strings.ToUpper(clean(priority))
	if !IsPriority(priority) {
		priority = PriorityNormal
	}
	number, err := s.store.NextTicketNumber(ctx)
	if err != nil {
		return Ticket{}, err
	}
	policy, err := s.PolicyFor(ctx, Ticket{Priority: priority})
	if err != nil {
		return Ticket{}, err
	}

	at := s.nowUnix()
	t := Ticket{
		ID:          newID("tkt"),
		OrgID:       session.OrgID,
		Number:      number,
		Title:       firstNonEmpty(truncate(clean(title), 200), firstNonEmpty(session.Subject, "Chat follow-up")),
		Description: truncate(transcript.String(), 20000),
		Priority:    priority,
		Status:      StateOpen,
		UserID:      session.UserID,
		SLAPolicyID: policy.ID,
		CreatedAt:   at,
		UpdatedAt:   at,
		Version:     1,
	}
	if err := s.store.InsertTicket(ctx, t); err != nil {
		return Ticket{}, err
	}
	// The agent who ran the chat keeps the ticket, and the ownership change
	// travels the same assignment path as any other claim.
	t, err = s.assign(ctx, id, t, agent.UserID, "escalated from chat")
	if err != nil {
		return Ticket{}, err
	}

	session.Status = ChatEscalated
	session.TicketID = t.ID
	session.EndedAt = at
	session.LastEventAt = at
	if err := s.store.UpdateChatSession(ctx, session); err != nil {
		return Ticket{}, err
	}
	if err := s.audit(ctx, id, AuditEntry{
		OrgID:      session.OrgID,
		Action:     "chat.escalate",
		EntityType: "chat",
		EntityID:   session.ID,
		FromState:  ChatActive,
		ToState:    ChatEscalated,
		Detail:     t.Number,
	}); err != nil {
		return Ticket{}, err
	}
	if err := s.audit(ctx, id, AuditEntry{
		OrgID:      t.OrgID,
		Action:     "ticket.escalate",
		EntityType: "ticket",
		EntityID:   t.ID,
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
