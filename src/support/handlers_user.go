package support

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/errors"
)

// This file holds the handlers a customer reaches. Every one of them serves
// both the browser and the API from the same code path, so the two can never
// drift apart, and every state-changing handler insists on POST plus a valid
// CSRF token before it reads a single submitted value.

// handleHelp shows the bot entry point. It is a read: no session row is
// created until the visitor actually says something.
func (s *Service) handleHelp(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	data := botData{Greeting: BotGreeting}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{
			"greeting":     BotGreeting,
			"max_attempts": BotMaxAttempts,
		})
		return
	}
	s.render(w, r, http.StatusOK, "bot", s.newView(r, id, "Support", data))
}

// handleBotMessage carries one turn of the conversation. The bot answers from
// the compiled rule table only; it never writes a ticket.
func (s *Service) handleBotMessage(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if err := requirePOST(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.readForm(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.checkCSRF(r, id); err != nil {
		s.fail(w, r, id, err)
		return
	}

	sessionID := formValue(r, "session")
	if sessionID == "" {
		started, err := s.StartBotSession(r.Context(), id)
		if err != nil {
			s.fail(w, r, id, err)
			return
		}
		sessionID = started.ID
	}

	text := formValue(r, "message")
	session, reply, err := s.BotMessage(r.Context(), id, sessionID, text)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}

	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{
			"session_id": session.ID,
			"matched":    reply.Matched,
			"answer":     reply.Answer,
			"attempts":   reply.Attempts,
			"exhausted":  reply.Exhausted,
			"prefill":    reply.Prefill,
		})
		return
	}

	data := botData{
		Session:  session,
		Turns:    TranscriptLines(session.Transcript),
		Reply:    reply,
		Greeting: BotGreeting,
		Answered: reply.Matched,
		Articles: s.SuggestArticles(text, 3),
	}
	s.render(w, r, http.StatusOK, "bot", s.newView(r, id, "Support", data))
}

// handleBotFeedback closes the conversation when the answer was enough. No
// ticket exists at this point and none is created.
func (s *Service) handleBotFeedback(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if err := requirePOST(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.readForm(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.checkCSRF(r, id); err != nil {
		s.fail(w, r, id, err)
		return
	}

	sessionID := r.PathValue("id")
	if !formBool(r, "helpful") {
		s.handleBotEscalate(w, r)
		return
	}
	if err := s.MarkBotResolved(r.Context(), id, sessionID); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{"resolved": true})
		return
	}
	s.renderMessage(w, r, id, http.StatusOK, "Support",
		"Glad that sorted it. Nothing was filed, so there is nothing for you to follow up.")
}

// handleBotEscalate hands the conversation to a person by preparing a draft
// ticket. The draft is not an open ticket: the user still has to submit it.
func (s *Service) handleBotEscalate(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if err := requirePOST(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if r.Form == nil {
		if err := s.readForm(r); err != nil {
			s.fail(w, r, id, err)
			return
		}
		if err := s.checkCSRF(r, id); err != nil {
			s.fail(w, r, id, err)
			return
		}
	}

	draft, prefill, err := s.EscalateBotSession(r.Context(), id, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{
			"draft_id":      draft.ID,
			"ticket_number": draft.Number,
			"prefill":       prefill,
		})
		return
	}
	s.redirect(w, r, "/tickets/"+draft.ID+"/edit")
}

// handleTicketForm renders the pre-filled draft. Every field stays editable.
func (s *Service) handleTicketForm(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	t, _, err := s.Ticket(r.Context(), id, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if t.Status != StateDraft {
		s.redirect(w, r, "/tickets/"+t.ID)
		return
	}
	categories, err := s.Categories(r.Context())
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	data := formData{
		Ticket:     t,
		Categories: categories,
		Priorities: ticketPriorities,
		Autosave:   DraftAutosaveSeconds,
	}
	s.render(w, r, http.StatusOK, "ticket-form", s.newView(r, id, "Your ticket", data))
}

// ticketInputFrom reads the ticket fields out of a submitted form.
func ticketInputFrom(r *http.Request) TicketInput {
	return TicketInput{
		Title:       formValue(r, "title"),
		Description: formValue(r, "description"),
		CategoryID:  formValue(r, "category_id"),
		Priority:    formValue(r, "priority"),
	}
}

// handleDraftSave stores the autosave. It never changes the ticket's state.
func (s *Service) handleDraftSave(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if err := requirePOST(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.readForm(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.checkCSRF(r, id); err != nil {
		s.fail(w, r, id, err)
		return
	}

	t, err := s.SaveDraft(r.Context(), id, r.PathValue("id"), ticketInputFrom(r))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{"draft_id": t.ID, "saved_at": t.UpdatedAt})
		return
	}
	s.redirect(w, r, "/tickets/"+t.ID+"/edit")
}

// handleTicketSubmit opens the ticket. This is the only path in the package
// that moves a draft to OPEN, and it runs only on an explicit user action.
func (s *Service) handleTicketSubmit(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if err := requirePOST(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.readForm(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.checkCSRF(r, id); err != nil {
		s.fail(w, r, id, err)
		return
	}

	t, err := s.SubmitTicket(r.Context(), id, r.PathValue("id"), ticketInputFrom(r))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusCreated, t)
		return
	}
	s.redirect(w, r, "/tickets/"+t.ID)
}

// handleTicketList shows the caller's own tickets.
func (s *Service) handleTicketList(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	page, limit := pageParams(r)
	tickets, p, err := s.ListTickets(r.Context(), id, TicketFilter{
		Search: strings.TrimSpace(r.URL.Query().Get("q")),
		Page:   page,
		Limit:  limit,
	})
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{"tickets": tickets, "page": p})
		return
	}
	s.render(w, r, http.StatusOK, "tickets",
		s.newView(r, id, "My tickets", listData{Tickets: tickets, Page: p, Now: s.nowUnix()}))
}

// handleTicketView shows one ticket to the person who raised it.
func (s *Service) handleTicketView(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	t, msgs, err := s.Ticket(r.Context(), id, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if t.Status == StateDraft {
		s.redirect(w, r, "/tickets/"+t.ID+"/edit")
		return
	}
	files, err := s.Attachments(r.Context(), id, t.ID)
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
	data := ticketData{
		Ticket:      t,
		Messages:    msgs,
		Attachments: files,
		Now:         s.nowUnix(),
		MaxKB:       s.settingInt(r.Context(), SettingAttachmentMaxKB),
	}
	s.render(w, r, http.StatusOK, "ticket", s.newView(r, id, t.Number, data))
}

// handleTicketReply adds the customer's reply.
func (s *Service) handleTicketReply(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if err := requirePOST(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.readForm(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.checkCSRF(r, id); err != nil {
		s.fail(w, r, id, err)
		return
	}

	t, err := s.AddUserReply(r.Context(), id, r.PathValue("id"), formValue(r, "body"))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusCreated, t)
		return
	}
	s.redirect(w, r, "/tickets/"+t.ID)
}

// handleTicketClose closes the caller's own ticket.
func (s *Service) handleTicketClose(w http.ResponseWriter, r *http.Request) {
	s.ticketStateAction(w, r, s.CloseTicket)
}

// handleTicketReopen reopens a closed ticket.
func (s *Service) handleTicketReopen(w http.ResponseWriter, r *http.Request) {
	s.ticketStateAction(w, r, s.ReopenTicket)
}

// ticketStateAction runs one of the customer's two state actions with the same
// method, CSRF and rendering rules.
func (s *Service) ticketStateAction(w http.ResponseWriter, r *http.Request, action func(context.Context, Identity, string) (Ticket, error)) {
	id := s.identity(r)
	if err := requirePOST(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.readForm(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.checkCSRF(r, id); err != nil {
		s.fail(w, r, id, err)
		return
	}

	t, err := action(r.Context(), id, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, t)
		return
	}
	s.redirect(w, r, "/tickets/"+t.ID)
}

// handleAttachmentUpload accepts one file for a ticket.
func (s *Service) handleAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if err := requirePOST(r); err != nil {
		s.fail(w, r, id, err)
		return
	}

	maxBytes := int64(s.settingInt(r.Context(), SettingAttachmentMaxKB)) * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+maxFormBytes)
	if err := r.ParseMultipartForm(maxFormBytes); err != nil {
		s.fail(w, r, id, errors.New(errors.CodePayloadTooLarge, 413, "That upload was too large"))
		return
	}
	if err := s.checkCSRF(r, id); err != nil {
		s.fail(w, r, id, err)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		s.fail(w, r, id, errors.New(errors.CodeValidation, 422, "Choose a file to upload"))
		return
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			s.logger().Debug("support upload close failed")
		}
	}()

	ticketID := r.PathValue("id")
	att, err := s.AttachFile(r.Context(), id, ticketID,
		header.Filename, header.Header.Get("Content-Type"), file)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusCreated, att)
		return
	}
	s.redirect(w, r, "/tickets/"+ticketID)
}

// handleAttachmentDownload streams one attachment back. The stored file is
// re-resolved through the safe join on every request and is always sent as an
// attachment so no tenant-supplied file is ever rendered in the browser.
func (s *Service) handleAttachmentDownload(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	att, body, err := s.OpenAttachment(r.Context(), id, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	defer func() {
		if closeErr := body.Close(); closeErr != nil {
			s.logger().Debug("support attachment close failed")
		}
	}()

	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", "attachment; filename=\""+downloadName(att.OriginalName)+"\"")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", time.Unix(att.CreatedAt, 0).UTC(), body)
}

// downloadName reduces a stored display name to characters that cannot break
// out of the quoted filename in a Content-Disposition header.
func downloadName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_', r == ' ':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "attachment"
	}
	return truncate(out, 100)
}

// handleChat shows live chat: either the availability notice and the start
// form, or the caller's open conversation.
func (s *Service) handleChat(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	avail, err := s.ChatAvailable(r.Context())
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, avail)
		return
	}
	s.render(w, r, http.StatusOK, "chat",
		s.newView(r, id, "Live chat", chatData{Availability: avail, Now: s.nowUnix()}))
}

// handleChatStart joins the queue.
func (s *Service) handleChatStart(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if err := requirePOST(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.readForm(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.checkCSRF(r, id); err != nil {
		s.fail(w, r, id, err)
		return
	}

	session, err := s.StartChat(r.Context(), id, formValue(r, "subject"))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusCreated, session)
		return
	}
	s.redirect(w, r, "/chats/"+session.ID)
}

// handleChatView shows one conversation from the customer's side.
func (s *Service) handleChatView(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	after := int64(queryInt(r, "after", 0))
	session, msgs, err := s.ChatView(r.Context(), id, r.PathValue("id"), after)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	position, err := s.QueuePosition(r.Context(), session)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{
			"session":  session,
			"messages": msgs,
			"position": position,
		})
		return
	}
	data := chatData{
		Session:  session,
		Messages: msgs,
		Position: position,
		Active:   true,
		Now:      s.nowUnix(),
	}
	s.render(w, r, http.StatusOK, "chat", s.newView(r, id, "Live chat", data))
}

// handleChatSend posts one customer message.
func (s *Service) handleChatSend(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if err := requirePOST(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.readForm(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.checkCSRF(r, id); err != nil {
		s.fail(w, r, id, err)
		return
	}

	sessionID := r.PathValue("id")
	msg, err := s.PostChatMessage(r.Context(), id, sessionID, formValue(r, "body"))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusCreated, msg)
		return
	}
	s.redirect(w, r, "/chats/"+sessionID)
}

// handleChatClose ends the conversation and records the optional rating.
func (s *Service) handleChatClose(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if err := requirePOST(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.readForm(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.checkCSRF(r, id); err != nil {
		s.fail(w, r, id, err)
		return
	}

	session, err := s.CloseChat(r.Context(), id, r.PathValue("id"), formInt(r, "rating", 0))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, session)
		return
	}
	s.renderMessage(w, r, id, http.StatusOK, "Live chat", "That chat is closed. Thank you.")
}

// handleKBIndex searches published articles.
func (s *Service) handleKBIndex(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	page, limit := pageParams(r)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	articles, p, err := s.SearchArticles(r.Context(), id, query, page, limit)
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{"articles": articles, "page": p})
		return
	}
	s.render(w, r, http.StatusOK, "kb-index",
		s.newView(r, id, "Knowledge base", kbIndexData{Query: query, Articles: articles, Page: p}))
}

// handleKBArticle shows one article.
func (s *Service) handleKBArticle(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	article, err := s.PublicArticle(r.Context(), id, r.PathValue("slug"))
	if err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, article)
		return
	}
	s.render(w, r, http.StatusOK, "kb-article",
		s.newView(r, id, article.Title, kbArticleData{Article: article}))
}

// handleKBFeedback records whether an article helped.
func (s *Service) handleKBFeedback(w http.ResponseWriter, r *http.Request) {
	id := s.identity(r)
	if err := requirePOST(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.readForm(r); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if err := s.checkCSRF(r, id); err != nil {
		s.fail(w, r, id, err)
		return
	}

	slug := r.PathValue("slug")
	if err := s.ArticleFeedback(r.Context(), id, slug, formBool(r, "helpful")); err != nil {
		s.fail(w, r, id, err)
		return
	}
	if s.wantsJSON(r) {
		s.writeJSON(w, http.StatusOK, map[string]any{"recorded": true})
		return
	}
	s.redirect(w, r, "/kb/"+slug)
}
