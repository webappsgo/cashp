package support

import (
	"net/http"
	"strconv"
	"strings"
)

// This file holds the agent workspace. Every handler here runs behind the agent
// check in the service layer, and every one that changes something insists on
// POST plus a valid CSRF token before it looks at a submitted value.

// beginMutation applies the three gates every state-changing request passes:
// the method, a bounded form parse and the session's CSRF token. It reports
// whether the handler may continue and has already answered the caller if not.
func (s *Service) beginMutation(w http.ResponseWriter, r *http.Request, id Identity) bool {
	if err := requirePOST(r); err != nil {
		s.fail(w, r, id, err)
		return false
	}
	if err := s.readForm(r); err != nil {
		s.fail(w, r, id, err)
		return false
	}
	if err := s.checkCSRF(r, id); err != nil {
		s.fail(w, r, id, err)
		return false
	}
	return true
}

// handleSupportModeEnter puts an agent into support mode. The reason is
// recorded and the whole session is time-boxed by SupportModeTTL.
func (s *Service) handleSupportModeEnter(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	mode, err := s.EnterSupportMode(r.Context(), id, formValue(r, "reason"))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, mode)
		return
	}
	s.redirect(w, r, "/agents/queue")
}

// handleSupportModeExit returns the agent to their own perspective.
func (s *Service) handleSupportModeExit(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	if err := s.ExitSupportMode(r.Context(), id); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{"in_support_mode": false})
		return
	}
	s.redirect(w, r, "")
}

// handleSupportModeStatus reports the current mode and the two counts the
// banner shows.
func (s *Service) handleSupportModeStatus(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	v := s.newView(r, id, "Support mode", nil)
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{
			"in_support_mode": v.InMode,
			"display_name":    v.Mode.DisplayName,
			"expires_at":      v.Mode.ExpiresAt,
			"queue_count":     v.QueueCount,
			"assigned_count":  v.MineCount,
		})
		return
	}
	s.redirect(w, r, "/agents/queue")
}

// handleQueue renders the shared ticket queue.
func (s *Service) handleQueue(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	page, limit := pageParams(r)
	items, p, err := s.Queue(r.Context(), id, TicketFilter{
		Search:   strings.TrimSpace(r.URL.Query().Get("q")),
		Priority: strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("priority"))),
		Page:     page,
		Limit:    limit,
	})
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	metrics, err := s.Metrics(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{
			"queue":   items,
			"page":    p,
			"metrics": metrics,
		})
		return
	}
	roster, err := s.Roster(r.Context())
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	data := queueData{Items: items, Page: p, Metrics: metrics, Roster: roster, Now: s.nowUnix()}
	s.render(w, r, http.StatusOK, "agent-queue", s.newView(r, id, "Ticket queue", data))
}

// handleAgentTicket renders one ticket with its internal notes, its audit trail
// and the responses the agent may insert.
func (s *Service) handleAgentTicket(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	ctx := r.Context()
	ticketID := r.PathValue("id")

	t, msgs, err := s.AgentTicket(ctx, id, ticketID)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	files, err := s.Attachments(ctx, id, t.ID)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{
			"ticket":      t,
			"messages":    msgs,
			"attachments": files,
		})
		return
	}

	trail, err := s.TicketAudit(ctx, id, t.ID)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	canned, err := s.CannedResponses(ctx, id)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	suggested, err := s.SuggestCanned(ctx, id, t.Title+" "+t.Description, 5)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	roster, err := s.Roster(ctx)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	policy, err := s.PolicyFor(ctx, t)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}

	data := agentTicketData{
		Ticket:      t,
		Messages:    msgs,
		Attachments: files,
		Audit:       trail,
		Risk:        EvaluateSLA(t, policy, s.nowUnix()),
		Canned:      canned,
		Suggested:   suggested,
		NextStates:  NextStates(t.Status, ActorAgent),
		Roster:      roster,
		Now:         s.nowUnix(),
	}
	s.render(w, r, http.StatusOK, "agent-ticket", s.newView(r, id, t.Number, data))
}

// handleClaim takes an unassigned ticket out of the queue.
func (s *Service) handleClaim(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	t, err := s.ClaimTicket(r.Context(), id, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, t)
		return
	}
	s.redirect(w, r, "/agents/tickets/"+t.ID)
}

// handleAssign hands a ticket to another agent and records who moved it.
func (s *Service) handleAssign(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	toUserID, err := strconv.ParseInt(strings.TrimSpace(formValue(r, "to_user_id")), 10, 64)
	if err != nil {
		toUserID = 0
	}
	t, assignErr := s.AssignTicket(r.Context(), id, r.PathValue("id"), toUserID, formValue(r, "reason"))
	if assignErr != nil {
		s.fail(w, r, id, assignErr)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, t)
		return
	}
	s.redirect(w, r, "/agents/tickets/"+t.ID)
}

// handleAgentReply posts either a customer-visible reply or an internal note.
// A canned response, when one is chosen, is expanded into the body here so the
// stored message is ordinary text and its usage count is recorded once.
func (s *Service) handleAgentReply(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	ctx := r.Context()
	body := formValue(r, "body")
	if cannedID := strings.TrimSpace(formValue(r, "canned_id")); cannedID != "" {
		canned, err := s.UseCanned(ctx, id, cannedID)
		if err != nil {
			s.fail(w, r, id, err)
			return
		}
		body = strings.TrimSpace(canned.Body + "\n\n" + body)
	}

	t, err := s.AgentReply(ctx, id, r.PathValue("id"), body, formBool(r, "internal"))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusCreated, t)
		return
	}
	s.redirect(w, r, "/agents/tickets/"+t.ID)
}

// handleAgentStatus drives one ticket transition. An illegal transition is
// rejected by the state machine, never applied and then corrected.
func (s *Service) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	t, err := s.AgentTransition(r.Context(), id, r.PathValue("id"),
		strings.ToUpper(strings.TrimSpace(formValue(r, "status"))), formValue(r, "reason"))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, t)
		return
	}
	s.redirect(w, r, "/agents/tickets/"+t.ID)
}

// handleAgentChats lists the waiting and active conversations.
func (s *Service) handleAgentChats(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	sessions, err := s.AgentChats(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
		return
	}
	data := agentChatsData{Sessions: sessions, Now: s.nowUnix()}
	s.render(w, r, http.StatusOK, "agent-chats", s.newView(r, id, "Live chat", data))
}

// handleAgentChatView opens one conversation alongside the waiting list.
func (s *Service) handleAgentChatView(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	ctx := r.Context()
	session, msgs, err := s.AgentChatView(ctx, id, r.PathValue("id"), int64(queryInt(r, "after", 0)))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{"session": session, "messages": msgs})
		return
	}
	sessions, err := s.AgentChats(ctx, id)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	data := agentChatsData{
		Sessions: sessions,
		Session:  session,
		Messages: msgs,
		Open:     true,
		Now:      s.nowUnix(),
	}
	s.render(w, r, http.StatusOK, "agent-chats", s.newView(r, id, "Live chat", data))
}

// handleChatAccept takes the next waiting conversation.
func (s *Service) handleChatAccept(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	session, err := s.AcceptChat(r.Context(), id, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, session)
		return
	}
	s.redirect(w, r, "/agents/chats/"+session.ID)
}

// handleAgentChatSend posts one agent message into a conversation.
func (s *Service) handleAgentChatSend(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	ctx := r.Context()
	sessionID := r.PathValue("id")
	body := formValue(r, "body")
	if cannedID := strings.TrimSpace(formValue(r, "canned_id")); cannedID != "" {
		canned, err := s.UseCanned(ctx, id, cannedID)
		if err != nil {
			s.fail(w, r, id, err)
			return
		}
		body = strings.TrimSpace(canned.Body + "\n\n" + body)
	}

	msg, err := s.AgentChatMessage(ctx, id, sessionID, body)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusCreated, msg)
		return
	}
	s.redirect(w, r, "/agents/chats/"+sessionID)
}

// handleAgentChatClose ends a conversation from the agent's side.
func (s *Service) handleAgentChatClose(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	session, err := s.CloseChat(r.Context(), id, r.PathValue("id"), 0)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, session)
		return
	}
	s.redirect(w, r, "/agents/chats")
}

// handleChatEscalate turns a conversation into a ticket, carrying the
// transcript across so the customer does not have to repeat themselves.
func (s *Service) handleChatEscalate(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	t, err := s.EscalateChat(r.Context(), id, r.PathValue("id"),
		formValue(r, "title"), strings.ToUpper(strings.TrimSpace(formValue(r, "priority"))))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusCreated, t)
		return
	}
	s.redirect(w, r, "/agents/tickets/"+t.ID)
}

// articleInputFrom reads the article fields out of a submitted form.
func articleInputFrom(r *http.Request) ArticleInput {
	return ArticleInput{
		Title:      formValue(r, "title"),
		Body:       formValue(r, "body"),
		Slug:       formValue(r, "slug"),
		CategoryID: formValue(r, "category_id"),
		Tags:       formValue(r, "tags"),
	}
}

// handleAgentArticles lists every article an agent may work on, including the
// drafts that no customer can see.
func (s *Service) handleAgentArticles(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	page, limit := pageParams(r)
	articles, p, err := s.StaffArticles(r.Context(), id, articleStatuses,
		strings.TrimSpace(r.URL.Query().Get("q")), page, limit)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{"articles": articles, "page": p})
		return
	}
	canned, err := s.CannedResponses(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	data := agentKBData{
		Articles: articles,
		Page:     p,
		Statuses: articleStatuses,
		CanPub:   s.RoleOf(r.Context(), id) == RoleAdmin,
		Canned:   canned,
	}
	s.render(w, r, http.StatusOK, "agent-kb", s.newView(r, id, "Knowledge base", data))
}

// handleArticleCreate adds a new draft.
func (s *Service) handleArticleCreate(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	article, err := s.CreateArticle(r.Context(), id, articleInputFrom(r))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusCreated, article)
		return
	}
	s.redirect(w, r, "/agents/articles/"+article.ID+"/edit")
}

// handleArticleEdit opens one article in the editor.
func (s *Service) handleArticleEdit(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	ctx := r.Context()
	articles, p, err := s.StaffArticles(ctx, id, articleStatuses, "", 1, MaxPageLimit)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}

	wanted := r.PathValue("id")
	var found Article
	for _, a := range articles {
		if a.ID == wanted {
			found = a
			break
		}
	}
	if found.ID == "" {
		s.fail(w, r, id, notFound("Article"))
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, found)
		return
	}
	canned, err := s.CannedResponses(ctx, id)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	data := agentKBData{
		Articles: articles,
		Page:     p,
		Article:  found,
		Editing:  true,
		Statuses: articleStatuses,
		CanPub:   s.RoleOf(ctx, id) == RoleAdmin,
		Canned:   canned,
	}
	s.render(w, r, http.StatusOK, "agent-kb", s.newView(r, id, found.Title, data))
}

// handleArticleSave stores an edit.
func (s *Service) handleArticleSave(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	article, err := s.EditArticle(r.Context(), id, r.PathValue("id"), articleInputFrom(r))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, article)
		return
	}
	s.redirect(w, r, "/agents/articles/"+article.ID+"/edit")
}

// handleArticleStatus moves an article along its lifecycle. Publishing is an
// administrator's decision and the service layer enforces that.
func (s *Service) handleArticleStatus(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	article, err := s.TransitionArticle(r.Context(), id, r.PathValue("id"),
		strings.ToUpper(strings.TrimSpace(formValue(r, "status"))))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, article)
		return
	}
	s.redirect(w, r, "/agents/articles/"+article.ID+"/edit")
}

// cannedInputFrom reads a canned response out of a submitted form.
func cannedInputFrom(r *http.Request) CannedInput {
	return CannedInput{
		Title:        formValue(r, "title"),
		Body:         formValue(r, "body"),
		Tags:         formValue(r, "tags"),
		Scope:        formValue(r, "scope"),
		DepartmentID: formValue(r, "department_id"),
	}
}

// handleCannedList returns the responses this agent may use: every system
// response, their department's, and their own personal ones.
func (s *Service) handleCannedList(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	ctx := r.Context()
	list, err := s.CannedResponses(ctx, id)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{"canned_responses": list})
		return
	}
	s.redirect(w, r, "/agents/articles")
}

// handleCannedCreate adds one personal response. An agent can only ever create
// a personal response; the shared tiers belong to administrators.
func (s *Service) handleCannedCreate(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	canned, err := s.CreatePersonalCanned(r.Context(), id, cannedInputFrom(r))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusCreated, canned)
		return
	}
	s.redirect(w, r, "/agents/articles")
}

// handleCannedUpdate edits one of the agent's own personal responses.
func (s *Service) handleCannedUpdate(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	canned, err := s.UpdatePersonalCanned(r.Context(), id, r.PathValue("id"), cannedInputFrom(r))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, canned)
		return
	}
	s.redirect(w, r, "/agents/articles")
}

// handleCannedDelete removes one of the agent's own personal responses.
func (s *Service) handleCannedDelete(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if !s.beginMutation(w, r, id) {
		return
	}
	if err := s.DeletePersonalCanned(r.Context(), id, r.PathValue("id")); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
		return
	}
	s.redirect(w, r, "/agents/articles")
}

// handleAgentMetrics returns the caller's own workload summary.
func (s *Service) handleAgentMetrics(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	metrics, err := s.Metrics(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, metrics)
		return
	}
	s.redirect(w, r, "/agents/queue")
}
