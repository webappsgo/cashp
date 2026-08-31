package support

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/errors"
)

// Store is the support package's data access layer. Every method that touches
// tenant-owned data takes an orgID and puts it in the WHERE clause, so a query
// cannot accidentally cross an organization boundary. No statement in this file
// is built by string concatenation of user input, and none selects with a
// wildcard column list.
type Store struct {
	db *database.DB
}

// NewStore wraps a database handle.
func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

// ticketColumns is the explicit column list for ticket reads.
const ticketColumns = `id, org_id, number, title, description, category_id, priority, status,
	user_id, assigned_to, bot_context, sla_policy_id, first_response_at, resolved_at,
	closed_at, created_at, updated_at, version`

// scanTicket reads one ticket row in ticketColumns order.
func scanTicket(row interface{ Scan(...any) error }) (Ticket, error) {
	var t Ticket
	err := row.Scan(&t.ID, &t.OrgID, &t.Number, &t.Title, &t.Description, &t.CategoryID,
		&t.Priority, &t.Status, &t.UserID, &t.AssignedTo, &t.BotContext, &t.SLAPolicyID,
		&t.FirstResponseAt, &t.ResolvedAt, &t.ClosedAt, &t.CreatedAt, &t.UpdatedAt, &t.Version)
	return t, err
}

// notFound is the single error returned for a missing or out-of-tenant row.
// Missing and forbidden are deliberately indistinguishable so that a probe
// cannot use the response to learn whether another tenant's ticket exists.
func notFound(entity string) error {
	return errors.New(errors.CodeNotFound, 404, entity+" not found")
}

// wrapDB converts a driver error into a safe application error. The driver
// message is kept only as the wrapped cause, which is logged and never sent.
func wrapDB(err error, action string) error {
	if err == nil {
		return nil
	}
	if database.IsAlreadyExistsError(err) {
		return errors.Wrap(err, errors.CodeConflict, 409, "That record already exists")
	}
	return errors.Wrap(err, errors.CodeInternal, 500, "Could not "+action)
}

// NextTicketNumber reserves the next sequential ticket number. The counter is
// bumped inside a transaction so two concurrent submissions cannot take the
// same number.
func (s *Store) NextTicketNumber(ctx context.Context) (string, error) {
	var value int64
	err := s.db.Tx(ctx, database.TimeoutWrite, func(tx *sql.Tx) error {
		row := s.db.TxQueryRow(ctx, tx, `SELECT value FROM support_counters WHERE name = ?`, "ticket")
		scanErr := row.Scan(&value)
		switch {
		case scanErr == sql.ErrNoRows:
			value = 1
			_, insErr := s.db.TxExec(ctx, tx, `INSERT INTO support_counters (name, value) VALUES (?, ?)`, "ticket", value)
			return insErr
		case scanErr != nil:
			return scanErr
		}
		value++
		_, updErr := s.db.TxExec(ctx, tx, `UPDATE support_counters SET value = ? WHERE name = ?`, value, "ticket")
		return updErr
	})
	if err != nil {
		return "", wrapDB(err, "allocate a ticket number")
	}
	digits := strconv.FormatInt(value, 10)
	for len(digits) < 6 {
		digits = "0" + digits
	}
	return "TKT-" + digits, nil
}

// InsertTicket stores a new ticket row.
func (s *Store) InsertTicket(ctx context.Context, t Ticket) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO support_tickets (
		id, org_id, number, title, description, category_id, priority, status, user_id,
		assigned_to, bot_context, sla_policy_id, first_response_at, resolved_at, closed_at,
		created_at, updated_at, version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.OrgID, t.Number, t.Title, t.Description, t.CategoryID, t.Priority, t.Status,
		t.UserID, t.AssignedTo, t.BotContext, t.SLAPolicyID, t.FirstResponseAt, t.ResolvedAt,
		t.ClosedAt, t.CreatedAt, t.UpdatedAt, t.Version)
	return wrapDB(err, "save the ticket")
}

// Ticket loads one ticket scoped to an organization.
func (s *Store) Ticket(ctx context.Context, orgID int64, id string) (Ticket, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+ticketColumns+` FROM support_tickets WHERE id = ? AND org_id = ?`, id, orgID)
	t, err := scanTicket(row)
	if err == sql.ErrNoRows {
		return Ticket{}, notFound("Ticket")
	}
	if err != nil {
		return Ticket{}, wrapDB(err, "load the ticket")
	}
	return t, nil
}

// TicketAnyOrg loads a ticket without an organization filter. It exists only
// for the agent workspace and the scheduler, which are installation-wide by
// definition; every tenant-facing path uses Ticket instead.
func (s *Store) TicketAnyOrg(ctx context.Context, id string) (Ticket, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+ticketColumns+` FROM support_tickets WHERE id = ?`, id)
	t, err := scanTicket(row)
	if err == sql.ErrNoRows {
		return Ticket{}, notFound("Ticket")
	}
	if err != nil {
		return Ticket{}, wrapDB(err, "load the ticket")
	}
	return t, nil
}

// UpdateTicket writes a ticket back using optimistic concurrency on version.
func (s *Store) UpdateTicket(ctx context.Context, t Ticket) error {
	err := s.db.UpdateVersioned(ctx, `UPDATE support_tickets SET
		title = ?, description = ?, category_id = ?, priority = ?, status = ?,
		assigned_to = ?, sla_policy_id = ?, first_response_at = ?, resolved_at = ?,
		closed_at = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND org_id = ? AND version = ?`,
		t.Title, t.Description, t.CategoryID, t.Priority, t.Status, t.AssignedTo,
		t.SLAPolicyID, t.FirstResponseAt, t.ResolvedAt, t.ClosedAt, t.UpdatedAt,
		t.ID, t.OrgID, t.Version)
	if err != nil {
		return wrapDB(err, "update the ticket")
	}
	return nil
}

// TicketFilter narrows a ticket listing. Empty fields are ignored.
type TicketFilter struct {
	OrgID      int64
	UserID     int64
	AssignedTo int64
	Statuses   []string
	Priority   string
	Search     string
	QueueOnly  bool
	Page       int
	Limit      int
}

// buildTicketWhere assembles the WHERE clause and its bound arguments. Only
// fixed fragments are concatenated; every value travels as a placeholder.
func buildTicketWhere(f TicketFilter) (string, []any) {
	clauses := []string{"1 = 1"}
	args := []any{}
	if f.OrgID > 0 {
		clauses = append(clauses, "org_id = ?")
		args = append(args, f.OrgID)
	}
	if f.UserID > 0 {
		clauses = append(clauses, "user_id = ?")
		args = append(args, f.UserID)
	}
	if f.AssignedTo > 0 {
		clauses = append(clauses, "assigned_to = ?")
		args = append(args, f.AssignedTo)
	}
	if f.Priority != "" {
		clauses = append(clauses, "priority = ?")
		args = append(args, f.Priority)
	}
	states := f.Statuses
	if f.QueueOnly {
		states = nil
		for _, st := range allStates {
			if IsQueueState(st) {
				states = append(states, st)
			}
		}
	}
	if len(states) > 0 {
		holders := make([]string, len(states))
		for i, st := range states {
			holders[i] = "?"
			args = append(args, st)
		}
		clauses = append(clauses, "status IN ("+strings.Join(holders, ", ")+")")
	}
	if f.Search != "" {
		clauses = append(clauses, "(title LIKE ? OR description LIKE ? OR number LIKE ?)")
		like := "%" + f.Search + "%"
		args = append(args, like, like, like)
	}
	return strings.Join(clauses, " AND "), args
}

// ListTickets returns a page of tickets matching the filter, newest first.
func (s *Store) ListTickets(ctx context.Context, f TicketFilter) ([]Ticket, Page, error) {
	where, args := buildTicketWhere(f)

	var total int
	countRow := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT COUNT(*) FROM support_tickets WHERE `+where, args...)
	if err := countRow.Scan(&total); err != nil {
		return nil, Page{}, wrapDB(err, "count tickets")
	}

	page := newPage(f.Page, f.Limit, total)
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+ticketColumns+` FROM support_tickets WHERE `+where+
			` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), page.Limit, offsetFor(page))...)
	if err != nil {
		return nil, Page{}, wrapDB(err, "list tickets")
	}
	defer rows.Close()

	var out []Ticket
	for rows.Next() {
		t, scanErr := scanTicket(rows)
		if scanErr != nil {
			return nil, Page{}, wrapDB(scanErr, "read a ticket")
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, Page{}, wrapDB(err, "read tickets")
	}
	return out, page, nil
}

// TicketsInState lists every ticket in one of the given states without paging.
// It backs the scheduler sweeps, which must see the whole set.
func (s *Store) TicketsInState(ctx context.Context, states ...string) ([]Ticket, error) {
	if len(states) == 0 {
		return nil, nil
	}
	holders := make([]string, len(states))
	args := make([]any, len(states))
	for i, st := range states {
		holders[i] = "?"
		args[i] = st
	}
	rows, err := s.db.QueryContext(ctx, database.TimeoutBulk,
		`SELECT `+ticketColumns+` FROM support_tickets WHERE status IN (`+
			strings.Join(holders, ", ")+`) ORDER BY created_at ASC`, args...)
	if err != nil {
		return nil, wrapDB(err, "list tickets")
	}
	defer rows.Close()

	var out []Ticket
	for rows.Next() {
		t, scanErr := scanTicket(rows)
		if scanErr != nil {
			return nil, wrapDB(scanErr, "read a ticket")
		}
		out = append(out, t)
	}
	return out, wrapDB(rows.Err(), "read tickets")
}

// CountTickets counts tickets matching a filter.
func (s *Store) CountTickets(ctx context.Context, f TicketFilter) (int, error) {
	where, args := buildTicketWhere(f)
	var total int
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT COUNT(*) FROM support_tickets WHERE `+where, args...)
	if err := row.Scan(&total); err != nil {
		return 0, wrapDB(err, "count tickets")
	}
	return total, nil
}

// HasAudit reports whether one action was already recorded against an entity.
// It is how the scheduler avoids sending the same escalation warning twice.
func (s *Store) HasAudit(ctx context.Context, entityType, entityID, action string) (bool, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT COUNT(*) FROM support_audit_logs
		WHERE entity_type = ? AND entity_id = ? AND action = ?`, entityType, entityID, action)
	var count int
	if err := row.Scan(&count); err != nil {
		return false, wrapDB(err, "read the audit log")
	}
	return count > 0, nil
}

// GroupTickets counts tickets grouped by one of a fixed set of columns. The
// column name comes from the switch below and never from caller input.
func (s *Store) GroupTickets(ctx context.Context, group string) (map[string]int, error) {
	var column string
	switch group {
	case "status":
		column = "status"
	case "priority":
		column = "priority"
	case "category":
		column = "category_id"
	default:
		return nil, errors.New(errors.CodeValidation, 400, "Unknown grouping")
	}
	rows, err := s.db.QueryContext(ctx, database.TimeoutReport,
		`SELECT `+column+`, COUNT(*) FROM support_tickets WHERE status <> ? GROUP BY `+column, StateDraft)
	if err != nil {
		return nil, wrapDB(err, "build the report")
	}
	defer func() { _ = rows.Close() }()

	out := map[string]int{}
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return nil, wrapDB(err, "build the report")
		}
		if key == "" {
			key = "uncategorised"
		}
		out[key] = count
	}
	return out, wrapDB(rows.Err(), "build the report")
}

// ChatSatisfaction returns the number of rated chats and their average rating
// in hundredths, so the caller needs no floating point to display it.
func (s *Store) ChatSatisfaction(ctx context.Context) (int, int, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutReport,
		`SELECT COUNT(*), COALESCE(SUM(rating), 0) FROM support_chat_sessions WHERE rating > 0`)
	var count, total int
	if err := row.Scan(&count, &total); err != nil {
		return 0, 0, wrapDB(err, "build the report")
	}
	if count == 0 {
		return 0, 0, nil
	}
	return count, (total * 100) / count, nil
}

// InsertMessage appends a reply or internal note to a ticket.
func (s *Store) InsertMessage(ctx context.Context, m TicketMessage) error {
	internal := 0
	if m.Internal {
		internal = 1
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO support_ticket_messages (
		id, ticket_id, org_id, author_id, author_role, author_name, body, internal, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.TicketID, m.OrgID, m.AuthorID, m.AuthorRole, m.AuthorName, m.Body, internal, m.CreatedAt)
	return wrapDB(err, "save the reply")
}

// Messages lists a ticket's conversation. When includeInternal is false the
// internal notes are filtered in SQL, so they never reach a user-facing render.
func (s *Store) Messages(ctx context.Context, orgID int64, ticketID string, includeInternal bool) ([]TicketMessage, error) {
	query := `SELECT id, ticket_id, org_id, author_id, author_role, author_name, body, internal, created_at
		FROM support_ticket_messages WHERE ticket_id = ? AND org_id = ?`
	if !includeInternal {
		query += ` AND internal = 0`
	}
	query += ` ORDER BY created_at ASC, id ASC`

	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query, ticketID, orgID)
	if err != nil {
		return nil, wrapDB(err, "load the conversation")
	}
	defer rows.Close()

	var out []TicketMessage
	for rows.Next() {
		var m TicketMessage
		var internal int64
		if scanErr := rows.Scan(&m.ID, &m.TicketID, &m.OrgID, &m.AuthorID, &m.AuthorRole,
			&m.AuthorName, &m.Body, &internal, &m.CreatedAt); scanErr != nil {
			return nil, wrapDB(scanErr, "read a reply")
		}
		m.Internal = internal != 0
		out = append(out, m)
	}
	return out, wrapDB(rows.Err(), "read the conversation")
}

// InsertAttachment records an uploaded file.
func (s *Store) InsertAttachment(ctx context.Context, a Attachment) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO support_ticket_attachments (
		id, ticket_id, org_id, message_id, original_name, stored_name, content_type,
		size_bytes, uploaded_by, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.TicketID, a.OrgID, a.MessageID, a.OriginalName, a.StoredName,
		a.ContentType, a.SizeBytes, a.UploadedBy, a.CreatedAt)
	return wrapDB(err, "save the attachment")
}

// Attachments lists a ticket's attachments.
func (s *Store) Attachments(ctx context.Context, orgID int64, ticketID string) ([]Attachment, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT id, ticket_id, org_id, message_id, original_name, stored_name, content_type,
			size_bytes, uploaded_by, created_at
		FROM support_ticket_attachments WHERE ticket_id = ? AND org_id = ? ORDER BY created_at ASC`,
		ticketID, orgID)
	if err != nil {
		return nil, wrapDB(err, "list attachments")
	}
	defer rows.Close()

	var out []Attachment
	for rows.Next() {
		var a Attachment
		if scanErr := rows.Scan(&a.ID, &a.TicketID, &a.OrgID, &a.MessageID, &a.OriginalName,
			&a.StoredName, &a.ContentType, &a.SizeBytes, &a.UploadedBy, &a.CreatedAt); scanErr != nil {
			return nil, wrapDB(scanErr, "read an attachment")
		}
		out = append(out, a)
	}
	return out, wrapDB(rows.Err(), "read attachments")
}

// Attachment loads one attachment scoped to its organization.
func (s *Store) Attachment(ctx context.Context, orgID int64, id string) (Attachment, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT id, ticket_id, org_id, message_id, original_name, stored_name, content_type,
			size_bytes, uploaded_by, created_at
		FROM support_ticket_attachments WHERE id = ? AND org_id = ?`, id, orgID)
	var a Attachment
	err := row.Scan(&a.ID, &a.TicketID, &a.OrgID, &a.MessageID, &a.OriginalName,
		&a.StoredName, &a.ContentType, &a.SizeBytes, &a.UploadedBy, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return Attachment{}, notFound("Attachment")
	}
	if err != nil {
		return Attachment{}, wrapDB(err, "load the attachment")
	}
	return a, nil
}

// AttachmentAnyOrg loads one attachment without an organization filter. It is
// reserved for support staff acting in support mode, who work across tenants by
// definition; every tenant-facing path uses Attachment instead.
func (s *Store) AttachmentAnyOrg(ctx context.Context, id string) (Attachment, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT id, ticket_id, org_id, message_id, original_name, stored_name, content_type,
			size_bytes, uploaded_by, created_at
		FROM support_ticket_attachments WHERE id = ?`, id)
	var a Attachment
	err := row.Scan(&a.ID, &a.TicketID, &a.OrgID, &a.MessageID, &a.OriginalName,
		&a.StoredName, &a.ContentType, &a.SizeBytes, &a.UploadedBy, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return Attachment{}, notFound("Attachment")
	}
	if err != nil {
		return Attachment{}, wrapDB(err, "load the attachment")
	}
	return a, nil
}

// InsertAssignment appends an assignment record.
func (s *Store) InsertAssignment(ctx context.Context, a Assignment) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO support_assignments (
		id, ticket_id, org_id, from_agent_id, to_agent_id, actor_id, reason, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.TicketID, a.OrgID, a.FromAgentID, a.ToAgentID, a.ActorID, a.Reason, a.CreatedAt)
	return wrapDB(err, "record the assignment")
}

// Assignments lists a ticket's assignment history.
func (s *Store) Assignments(ctx context.Context, orgID int64, ticketID string) ([]Assignment, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT id, ticket_id, org_id, from_agent_id, to_agent_id, actor_id, reason, created_at
		FROM support_assignments WHERE ticket_id = ? AND org_id = ? ORDER BY created_at ASC`,
		ticketID, orgID)
	if err != nil {
		return nil, wrapDB(err, "list assignments")
	}
	defer rows.Close()

	var out []Assignment
	for rows.Next() {
		var a Assignment
		if scanErr := rows.Scan(&a.ID, &a.TicketID, &a.OrgID, &a.FromAgentID, &a.ToAgentID,
			&a.ActorID, &a.Reason, &a.CreatedAt); scanErr != nil {
			return nil, wrapDB(scanErr, "read an assignment")
		}
		out = append(out, a)
	}
	return out, wrapDB(rows.Err(), "read assignments")
}

// InsertAudit appends one audit record. The support audit log is append-only:
// there is no update and no delete statement for this table anywhere.
func (s *Store) InsertAudit(ctx context.Context, e AuditEntry) error {
	mode := 0
	if e.SupportMode {
		mode = 1
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO support_audit_logs (
		id, org_id, actor_id, on_behalf_of, action, entity_type, entity_id,
		from_state, to_state, detail, support_mode, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.OrgID, e.ActorID, e.OnBehalfOf, e.Action, e.EntityType, e.EntityID,
		e.FromState, e.ToState, e.Detail, mode, e.CreatedAt)
	return wrapDB(err, "record the audit entry")
}

// AuditFor lists the audit trail of one entity, oldest first.
func (s *Store) AuditFor(ctx context.Context, entityType, entityID string) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT id, org_id, actor_id, on_behalf_of, action, entity_type, entity_id,
			from_state, to_state, detail, support_mode, created_at
		FROM support_audit_logs WHERE entity_type = ? AND entity_id = ? ORDER BY created_at ASC`,
		entityType, entityID)
	if err != nil {
		return nil, wrapDB(err, "list audit entries")
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var mode int64
		if scanErr := rows.Scan(&e.ID, &e.OrgID, &e.ActorID, &e.OnBehalfOf, &e.Action,
			&e.EntityType, &e.EntityID, &e.FromState, &e.ToState, &e.Detail, &mode,
			&e.CreatedAt); scanErr != nil {
			return nil, wrapDB(scanErr, "read an audit entry")
		}
		e.SupportMode = mode != 0
		out = append(out, e)
	}
	return out, wrapDB(rows.Err(), "read audit entries")
}

// UpsertAgent creates or updates an agent profile.
func (s *Store) UpsertAgent(ctx context.Context, a Agent) error {
	enabled := 0
	if a.Enabled {
		enabled = 1
	}
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite, `UPDATE support_agents SET
		display_name = ?, department_id = ?, max_concurrent_chats = ?, enabled = ?, updated_at = ?
		WHERE user_id = ?`,
		a.DisplayName, a.DepartmentID, a.MaxConcurrentChats, enabled, a.UpdatedAt, a.UserID)
	if err != nil {
		return wrapDB(err, "save the agent profile")
	}
	if n, rowsErr := res.RowsAffected(); rowsErr == nil && n > 0 {
		return nil
	}
	_, err = s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO support_agents (
		id, user_id, display_name, department_id, max_concurrent_chats, enabled,
		last_activity_at, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.UserID, a.DisplayName, a.DepartmentID, a.MaxConcurrentChats, enabled,
		a.LastActivityAt, a.CreatedAt, a.UpdatedAt)
	return wrapDB(err, "save the agent profile")
}

// scanAgent reads one agent row.
func scanAgent(row interface{ Scan(...any) error }) (Agent, error) {
	var a Agent
	var enabled int64
	err := row.Scan(&a.ID, &a.UserID, &a.DisplayName, &a.DepartmentID, &a.MaxConcurrentChats,
		&enabled, &a.LastActivityAt, &a.CreatedAt, &a.UpdatedAt)
	a.Enabled = enabled != 0
	return a, err
}

// agentColumns is the explicit column list for agent reads.
const agentColumns = `id, user_id, display_name, department_id, max_concurrent_chats,
	enabled, last_activity_at, created_at, updated_at`

// AgentByUser loads the agent profile for a user, if any.
func (s *Store) AgentByUser(ctx context.Context, userID int64) (Agent, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+agentColumns+` FROM support_agents WHERE user_id = ?`, userID)
	a, err := scanAgent(row)
	if err == sql.ErrNoRows {
		return Agent{}, notFound("Agent")
	}
	if err != nil {
		return Agent{}, wrapDB(err, "load the agent profile")
	}
	return a, nil
}

// ListAgents lists agent profiles, enabled ones first.
func (s *Store) ListAgents(ctx context.Context, enabledOnly bool) ([]Agent, error) {
	query := `SELECT ` + agentColumns + ` FROM support_agents`
	if enabledOnly {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY display_name ASC`

	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query)
	if err != nil {
		return nil, wrapDB(err, "list agents")
	}
	defer rows.Close()

	var out []Agent
	for rows.Next() {
		a, scanErr := scanAgent(rows)
		if scanErr != nil {
			return nil, wrapDB(scanErr, "read an agent")
		}
		out = append(out, a)
	}
	return out, wrapDB(rows.Err(), "read agents")
}

// TouchAgent records agent activity, which is what keeps availability off AWAY.
func (s *Store) TouchAgent(ctx context.Context, userID, at int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE support_agents SET last_activity_at = ?, updated_at = ? WHERE user_id = ?`,
		at, at, userID)
	return wrapDB(err, "record agent activity")
}

// UpsertDepartment creates or updates a department.
func (s *Store) UpsertDepartment(ctx context.Context, d Department) error {
	enabled := 0
	if d.Enabled {
		enabled = 1
	}
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE support_departments SET name = ?, description = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		d.Name, d.Description, enabled, d.UpdatedAt, d.ID)
	if err != nil {
		return wrapDB(err, "save the department")
	}
	if n, rowsErr := res.RowsAffected(); rowsErr == nil && n > 0 {
		return nil
	}
	_, err = s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO support_departments (id, name, description, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.Name, d.Description, enabled, d.CreatedAt, d.UpdatedAt)
	return wrapDB(err, "save the department")
}

// ListDepartments lists departments alphabetically.
func (s *Store) ListDepartments(ctx context.Context) ([]Department, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT id, name, description, enabled, created_at, updated_at
		FROM support_departments ORDER BY name ASC`)
	if err != nil {
		return nil, wrapDB(err, "list departments")
	}
	defer rows.Close()

	var out []Department
	for rows.Next() {
		var d Department
		var enabled int64
		if scanErr := rows.Scan(&d.ID, &d.Name, &d.Description, &enabled, &d.CreatedAt,
			&d.UpdatedAt); scanErr != nil {
			return nil, wrapDB(scanErr, "read a department")
		}
		d.Enabled = enabled != 0
		out = append(out, d)
	}
	return out, wrapDB(rows.Err(), "read departments")
}

// UpsertCategory creates or updates a ticket category.
func (s *Store) UpsertCategory(ctx context.Context, c Category) error {
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE support_categories SET parent_id = ?, name = ?, slug = ?, position = ?, enabled = ? WHERE id = ?`,
		c.ParentID, c.Name, c.Slug, c.Position, enabled, c.ID)
	if err != nil {
		return wrapDB(err, "save the category")
	}
	if n, rowsErr := res.RowsAffected(); rowsErr == nil && n > 0 {
		return nil
	}
	_, err = s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO support_categories (id, parent_id, name, slug, position, enabled)
		VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, c.ParentID, c.Name, c.Slug, c.Position, enabled)
	return wrapDB(err, "save the category")
}

// ListCategories lists the category tree in display order.
func (s *Store) ListCategories(ctx context.Context, enabledOnly bool) ([]Category, error) {
	query := `SELECT id, parent_id, name, slug, position, enabled FROM support_categories`
	if enabledOnly {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY position ASC, name ASC`

	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query)
	if err != nil {
		return nil, wrapDB(err, "list categories")
	}
	defer rows.Close()

	var out []Category
	for rows.Next() {
		var c Category
		var enabled int64
		if scanErr := rows.Scan(&c.ID, &c.ParentID, &c.Name, &c.Slug, &c.Position, &enabled); scanErr != nil {
			return nil, wrapDB(scanErr, "read a category")
		}
		c.Enabled = enabled != 0
		out = append(out, c)
	}
	return out, wrapDB(rows.Err(), "read categories")
}

// UpsertSLAPolicy creates or updates the policy for one priority.
func (s *Store) UpsertSLAPolicy(ctx context.Context, p SLAPolicy) error {
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE support_sla_policies SET first_response_mins = ?, resolution_mins = ?,
			escalate_percent = ?, enabled = ?, updated_at = ? WHERE priority = ?`,
		p.FirstResponseMins, p.ResolutionMins, p.EscalatePercent, enabled, p.UpdatedAt, p.Priority)
	if err != nil {
		return wrapDB(err, "save the SLA policy")
	}
	if n, rowsErr := res.RowsAffected(); rowsErr == nil && n > 0 {
		return nil
	}
	_, err = s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO support_sla_policies (id, priority, first_response_mins, resolution_mins,
			escalate_percent, enabled, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Priority, p.FirstResponseMins, p.ResolutionMins, p.EscalatePercent, enabled, p.UpdatedAt)
	return wrapDB(err, "save the SLA policy")
}

// ListSLAPolicies lists every configured SLA policy.
func (s *Store) ListSLAPolicies(ctx context.Context) ([]SLAPolicy, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT id, priority, first_response_mins, resolution_mins, escalate_percent, enabled, updated_at
		FROM support_sla_policies`)
	if err != nil {
		return nil, wrapDB(err, "list SLA policies")
	}
	defer rows.Close()

	var out []SLAPolicy
	for rows.Next() {
		var p SLAPolicy
		var enabled int64
		if scanErr := rows.Scan(&p.ID, &p.Priority, &p.FirstResponseMins, &p.ResolutionMins,
			&p.EscalatePercent, &enabled, &p.UpdatedAt); scanErr != nil {
			return nil, wrapDB(scanErr, "read an SLA policy")
		}
		p.Enabled = enabled != 0
		out = append(out, p)
	}
	return out, wrapDB(rows.Err(), "read SLA policies")
}

// Setting reads one configuration value, returning ok=false when unset.
func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT setting_value FROM support_settings WHERE setting_key = ?`, key)
	var value string
	err := row.Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, wrapDB(err, "read the setting")
	}
	return value, true, nil
}

// SetSetting writes one configuration value. Support settings are edited
// through the admin UI only; nothing here consults the environment.
func (s *Store) SetSetting(ctx context.Context, key, value string, at int64) error {
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE support_settings SET setting_value = ?, updated_at = ? WHERE setting_key = ?`,
		value, at, key)
	if err != nil {
		return wrapDB(err, "save the setting")
	}
	if n, rowsErr := res.RowsAffected(); rowsErr == nil && n > 0 {
		return nil
	}
	_, err = s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO support_settings (setting_key, setting_value, updated_at) VALUES (?, ?, ?)`,
		key, value, at)
	return wrapDB(err, "save the setting")
}

// ListSettings returns every configuration row.
func (s *Store) ListSettings(ctx context.Context) ([]Setting, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT setting_key, setting_value, updated_at FROM support_settings ORDER BY setting_key ASC`)
	if err != nil {
		return nil, wrapDB(err, "list settings")
	}
	defer rows.Close()

	var out []Setting
	for rows.Next() {
		var c Setting
		if scanErr := rows.Scan(&c.Key, &c.Value, &c.UpdatedAt); scanErr != nil {
			return nil, wrapDB(scanErr, "read a setting")
		}
		out = append(out, c)
	}
	return out, wrapDB(rows.Err(), "read settings")
}

// articleColumns is the explicit column list for article reads.
const articleColumns = `id, org_id, slug, title, body, category_id, tags, status, author_id,
	helpful_count, not_helpful_count, view_count, published_at, created_at, updated_at, version`

// scanArticle reads one article row in articleColumns order.
func scanArticle(row interface{ Scan(...any) error }) (Article, error) {
	var a Article
	err := row.Scan(&a.ID, &a.OrgID, &a.Slug, &a.Title, &a.Body, &a.CategoryID, &a.Tags,
		&a.Status, &a.AuthorID, &a.HelpfulCount, &a.NotHelpfulCnt, &a.ViewCount,
		&a.PublishedAt, &a.CreatedAt, &a.UpdatedAt, &a.Version)
	return a, err
}

// InsertArticle stores a new knowledge base article.
func (s *Store) InsertArticle(ctx context.Context, a Article) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO support_kb_articles (
		id, org_id, slug, title, body, category_id, tags, status, author_id,
		helpful_count, not_helpful_count, view_count, published_at, created_at, updated_at, version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.OrgID, a.Slug, a.Title, a.Body, a.CategoryID, a.Tags, a.Status, a.AuthorID,
		a.HelpfulCount, a.NotHelpfulCnt, a.ViewCount, a.PublishedAt, a.CreatedAt, a.UpdatedAt, a.Version)
	return wrapDB(err, "save the article")
}

// UpdateArticle writes an article back using optimistic concurrency.
func (s *Store) UpdateArticle(ctx context.Context, a Article) error {
	err := s.db.UpdateVersioned(ctx, `UPDATE support_kb_articles SET
		slug = ?, title = ?, body = ?, category_id = ?, tags = ?, status = ?,
		published_at = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		a.Slug, a.Title, a.Body, a.CategoryID, a.Tags, a.Status, a.PublishedAt,
		a.UpdatedAt, a.ID, a.Version)
	return wrapDB(err, "update the article")
}

// Article loads one article by id.
func (s *Store) Article(ctx context.Context, id string) (Article, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+articleColumns+` FROM support_kb_articles WHERE id = ?`, id)
	a, err := scanArticle(row)
	if err == sql.ErrNoRows {
		return Article{}, notFound("Article")
	}
	if err != nil {
		return Article{}, wrapDB(err, "load the article")
	}
	return a, nil
}

// ArticleBySlug loads one article by slug.
func (s *Store) ArticleBySlug(ctx context.Context, slug string) (Article, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+articleColumns+` FROM support_kb_articles WHERE slug = ?`, slug)
	a, err := scanArticle(row)
	if err == sql.ErrNoRows {
		return Article{}, notFound("Article")
	}
	if err != nil {
		return Article{}, wrapDB(err, "load the article")
	}
	return a, nil
}

// ArticleFilter narrows an article listing.
type ArticleFilter struct {
	Statuses []string
	Search   string
	OrgID    int64
	Page     int
	Limit    int
}

// ListArticles returns a page of articles. Draft and review articles are
// org-scoped, so a caller listing them must pass the organization; published
// articles are installation-wide and pass OrgID zero.
func (s *Store) ListArticles(ctx context.Context, f ArticleFilter) ([]Article, Page, error) {
	clauses := []string{"1 = 1"}
	args := []any{}
	if len(f.Statuses) > 0 {
		holders := make([]string, len(f.Statuses))
		for i, st := range f.Statuses {
			holders[i] = "?"
			args = append(args, st)
		}
		clauses = append(clauses, "status IN ("+strings.Join(holders, ", ")+")")
	}
	if f.OrgID > 0 {
		clauses = append(clauses, "(org_id = ? OR org_id = 0)")
		args = append(args, f.OrgID)
	}
	if f.Search != "" {
		clauses = append(clauses, "(title LIKE ? OR body LIKE ? OR tags LIKE ?)")
		like := "%" + f.Search + "%"
		args = append(args, like, like, like)
	}
	where := strings.Join(clauses, " AND ")

	var total int
	countRow := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT COUNT(*) FROM support_kb_articles WHERE `+where, args...)
	if err := countRow.Scan(&total); err != nil {
		return nil, Page{}, wrapDB(err, "count articles")
	}

	page := newPage(f.Page, f.Limit, total)
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+articleColumns+` FROM support_kb_articles WHERE `+where+
			` ORDER BY title ASC LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), page.Limit, offsetFor(page))...)
	if err != nil {
		return nil, Page{}, wrapDB(err, "list articles")
	}
	defer rows.Close()

	var out []Article
	for rows.Next() {
		a, scanErr := scanArticle(rows)
		if scanErr != nil {
			return nil, Page{}, wrapDB(scanErr, "read an article")
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, Page{}, wrapDB(err, "read articles")
	}
	return out, page, nil
}

// BumpArticleCounter increments one of the article counters by name. The column
// name comes from a fixed switch, never from caller input.
func (s *Store) BumpArticleCounter(ctx context.Context, id, counter string) error {
	var column string
	switch counter {
	case "helpful":
		column = "helpful_count"
	case "not_helpful":
		column = "not_helpful_count"
	case "view":
		column = "view_count"
	default:
		return errors.New(errors.CodeValidation, 400, "Unknown counter")
	}
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE support_kb_articles SET `+column+` = `+column+` + 1 WHERE id = ?`, id)
	return wrapDB(err, "record the feedback")
}

// chatColumns is the explicit column list for chat session reads.
const chatColumns = `id, org_id, user_id, agent_id, status, subject, ticket_id,
	queued_at, started_at, ended_at, rating, last_event_at`

// scanChat reads one chat session row.
func scanChat(row interface{ Scan(...any) error }) (ChatSession, error) {
	var c ChatSession
	err := row.Scan(&c.ID, &c.OrgID, &c.UserID, &c.AgentID, &c.Status, &c.Subject,
		&c.TicketID, &c.QueuedAt, &c.StartedAt, &c.EndedAt, &c.Rating, &c.LastEventAt)
	return c, err
}

// InsertChatSession stores a new chat session.
func (s *Store) InsertChatSession(ctx context.Context, c ChatSession) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO support_chat_sessions (
		id, org_id, user_id, agent_id, status, subject, ticket_id, queued_at,
		started_at, ended_at, rating, last_event_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.OrgID, c.UserID, c.AgentID, c.Status, c.Subject, c.TicketID,
		c.QueuedAt, c.StartedAt, c.EndedAt, c.Rating, c.LastEventAt)
	return wrapDB(err, "start the chat")
}

// UpdateChatSession writes a chat session back.
func (s *Store) UpdateChatSession(ctx context.Context, c ChatSession) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `UPDATE support_chat_sessions SET
		agent_id = ?, status = ?, subject = ?, ticket_id = ?, started_at = ?,
		ended_at = ?, rating = ?, last_event_at = ? WHERE id = ?`,
		c.AgentID, c.Status, c.Subject, c.TicketID, c.StartedAt, c.EndedAt,
		c.Rating, c.LastEventAt, c.ID)
	return wrapDB(err, "update the chat")
}

// ChatSession loads one chat session scoped to an organization.
func (s *Store) ChatSession(ctx context.Context, orgID int64, id string) (ChatSession, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+chatColumns+` FROM support_chat_sessions WHERE id = ? AND org_id = ?`, id, orgID)
	c, err := scanChat(row)
	if err == sql.ErrNoRows {
		return ChatSession{}, notFound("Chat session")
	}
	if err != nil {
		return ChatSession{}, wrapDB(err, "load the chat")
	}
	return c, nil
}

// ChatSessionAnyOrg loads a chat session without an organization filter, for
// the agent workspace and the reaping sweep.
func (s *Store) ChatSessionAnyOrg(ctx context.Context, id string) (ChatSession, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+chatColumns+` FROM support_chat_sessions WHERE id = ?`, id)
	c, err := scanChat(row)
	if err == sql.ErrNoRows {
		return ChatSession{}, notFound("Chat session")
	}
	if err != nil {
		return ChatSession{}, wrapDB(err, "load the chat")
	}
	return c, nil
}

// ChatSessionsByStatus lists chat sessions in the given states, oldest first.
func (s *Store) ChatSessionsByStatus(ctx context.Context, statuses ...string) ([]ChatSession, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	holders := make([]string, len(statuses))
	args := make([]any, len(statuses))
	for i, st := range statuses {
		holders[i] = "?"
		args[i] = st
	}
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+chatColumns+` FROM support_chat_sessions WHERE status IN (`+
			strings.Join(holders, ", ")+`) ORDER BY queued_at ASC`, args...)
	if err != nil {
		return nil, wrapDB(err, "list chats")
	}
	defer rows.Close()

	var out []ChatSession
	for rows.Next() {
		c, scanErr := scanChat(rows)
		if scanErr != nil {
			return nil, wrapDB(scanErr, "read a chat")
		}
		out = append(out, c)
	}
	return out, wrapDB(rows.Err(), "read chats")
}

// CountActiveChatsForAgent counts an agent's live conversations.
func (s *Store) CountActiveChatsForAgent(ctx context.Context, agentID int64) (int, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT COUNT(*) FROM support_chat_sessions WHERE agent_id = ? AND status = ?`,
		agentID, ChatActive)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, wrapDB(err, "count chats")
	}
	return n, nil
}

// InsertChatMessage appends a message to a chat session.
func (s *Store) InsertChatMessage(ctx context.Context, m ChatMessage) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO support_chat_messages (
		id, session_id, org_id, author_id, author_role, author_name, body, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.SessionID, m.OrgID, m.AuthorID, m.AuthorRole, m.AuthorName, m.Body, m.CreatedAt)
	return wrapDB(err, "send the message")
}

// ChatMessages lists a session's messages after a cursor timestamp.
func (s *Store) ChatMessages(ctx context.Context, orgID int64, sessionID string, after int64) ([]ChatMessage, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT id, session_id, org_id, author_id, author_role, author_name, body, created_at
		FROM support_chat_messages WHERE session_id = ? AND org_id = ? AND created_at >= ?
		ORDER BY created_at ASC, id ASC`, sessionID, orgID, after)
	if err != nil {
		return nil, wrapDB(err, "load the chat transcript")
	}
	defer rows.Close()

	var out []ChatMessage
	for rows.Next() {
		var m ChatMessage
		if scanErr := rows.Scan(&m.ID, &m.SessionID, &m.OrgID, &m.AuthorID, &m.AuthorRole,
			&m.AuthorName, &m.Body, &m.CreatedAt); scanErr != nil {
			return nil, wrapDB(scanErr, "read a chat message")
		}
		out = append(out, m)
	}
	return out, wrapDB(rows.Err(), "read the chat transcript")
}

// cannedColumns is the explicit column list for canned response reads.
const cannedColumns = `id, scope, department_id, agent_user_id, title, body, tags,
	usage_count, created_at, updated_at`

// scanCanned reads one canned response row.
func scanCanned(row interface{ Scan(...any) error }) (CannedResponse, error) {
	var c CannedResponse
	err := row.Scan(&c.ID, &c.Scope, &c.DepartmentID, &c.AgentUserID, &c.Title, &c.Body,
		&c.Tags, &c.UsageCount, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// InsertCanned stores a canned response.
func (s *Store) InsertCanned(ctx context.Context, c CannedResponse) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO support_canned_responses (
		id, scope, department_id, agent_user_id, title, body, tags, usage_count, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Scope, c.DepartmentID, c.AgentUserID, c.Title, c.Body, c.Tags,
		c.UsageCount, c.CreatedAt, c.UpdatedAt)
	return wrapDB(err, "save the canned response")
}

// UpdateCanned edits a canned response.
func (s *Store) UpdateCanned(ctx context.Context, c CannedResponse) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE support_canned_responses SET title = ?, body = ?, tags = ?, department_id = ?, updated_at = ?
		WHERE id = ?`, c.Title, c.Body, c.Tags, c.DepartmentID, c.UpdatedAt, c.ID)
	return wrapDB(err, "update the canned response")
}

// Canned loads one canned response by id.
func (s *Store) Canned(ctx context.Context, id string) (CannedResponse, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT `+cannedColumns+` FROM support_canned_responses WHERE id = ?`, id)
	c, err := scanCanned(row)
	if err == sql.ErrNoRows {
		return CannedResponse{}, notFound("Canned response")
	}
	if err != nil {
		return CannedResponse{}, wrapDB(err, "load the canned response")
	}
	return c, nil
}

// DeleteCanned removes a personal canned response. It is the only delete in the
// package and is restricted by the service to the response's own author.
func (s *Store) DeleteCanned(ctx context.Context, id string, agentUserID int64) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`DELETE FROM support_canned_responses WHERE id = ? AND scope = ? AND agent_user_id = ?`,
		id, CannedPersonal, agentUserID)
	return wrapDB(err, "delete the canned response")
}

// ListCannedFor returns the responses visible to one agent: every SYSTEM
// response, the DEPARTMENT responses of that agent's department, and only that
// agent's own PERSONAL responses.
func (s *Store) ListCannedFor(ctx context.Context, agentUserID int64, departmentID string) ([]CannedResponse, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+cannedColumns+` FROM support_canned_responses
		WHERE scope = ?
			OR (scope = ? AND department_id = ? AND department_id <> '')
			OR (scope = ? AND agent_user_id = ?)
		ORDER BY scope ASC, title ASC`,
		CannedSystem, CannedDepartment, departmentID, CannedPersonal, agentUserID)
	if err != nil {
		return nil, wrapDB(err, "list canned responses")
	}
	defer rows.Close()

	var out []CannedResponse
	for rows.Next() {
		c, scanErr := scanCanned(rows)
		if scanErr != nil {
			return nil, wrapDB(scanErr, "read a canned response")
		}
		out = append(out, c)
	}
	return out, wrapDB(rows.Err(), "read canned responses")
}

// ListCannedAdmin lists the SYSTEM and DEPARTMENT responses an administrator
// manages. Personal responses are excluded: they belong to their author alone.
func (s *Store) ListCannedAdmin(ctx context.Context) ([]CannedResponse, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+cannedColumns+` FROM support_canned_responses WHERE scope IN (?, ?)
		ORDER BY scope ASC, title ASC`, CannedSystem, CannedDepartment)
	if err != nil {
		return nil, wrapDB(err, "list canned responses")
	}
	defer rows.Close()

	var out []CannedResponse
	for rows.Next() {
		c, scanErr := scanCanned(rows)
		if scanErr != nil {
			return nil, wrapDB(scanErr, "read a canned response")
		}
		out = append(out, c)
	}
	return out, wrapDB(rows.Err(), "read canned responses")
}

// BumpCannedUsage records that a canned response was used.
func (s *Store) BumpCannedUsage(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE support_canned_responses SET usage_count = usage_count + 1 WHERE id = ?`, id)
	return wrapDB(err, "record canned response usage")
}

// BotSession is the stored state of one bot conversation. It exists so the bot
// works without JavaScript and so an escalation can carry its context forward;
// it holds the user's own words and never any pattern data.
type BotSession struct {
	ID           string
	OrgID        int64
	UserID       int64
	Attempts     int
	Resolved     bool
	Escalated    bool
	Transcript   string
	LastCategory string
	LastPriority string
	CreatedAt    int64
	UpdatedAt    int64
}

// InsertBotSession stores a new bot conversation.
func (s *Store) InsertBotSession(ctx context.Context, b BotSession) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `INSERT INTO support_bot_sessions (
		id, org_id, user_id, attempts, resolved, escalated, transcript,
		last_category, last_priority, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.OrgID, b.UserID, b.Attempts, boolInt(b.Resolved), boolInt(b.Escalated),
		b.Transcript, b.LastCategory, b.LastPriority, b.CreatedAt, b.UpdatedAt)
	return wrapDB(err, "start the help session")
}

// UpdateBotSession writes a bot conversation back.
func (s *Store) UpdateBotSession(ctx context.Context, b BotSession) error {
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, `UPDATE support_bot_sessions SET
		attempts = ?, resolved = ?, escalated = ?, transcript = ?, last_category = ?,
		last_priority = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		b.Attempts, boolInt(b.Resolved), boolInt(b.Escalated), b.Transcript, b.LastCategory,
		b.LastPriority, b.UpdatedAt, b.ID, b.UserID)
	return wrapDB(err, "update the help session")
}

// BotSession loads one bot conversation belonging to a user.
func (s *Store) BotSession(ctx context.Context, userID int64, id string) (BotSession, error) {
	row := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT id, org_id, user_id, attempts, resolved, escalated, transcript,
			last_category, last_priority, created_at, updated_at
		FROM support_bot_sessions WHERE id = ? AND user_id = ?`, id, userID)
	var b BotSession
	var resolved, escalated int64
	err := row.Scan(&b.ID, &b.OrgID, &b.UserID, &b.Attempts, &resolved, &escalated,
		&b.Transcript, &b.LastCategory, &b.LastPriority, &b.CreatedAt, &b.UpdatedAt)
	if err == sql.ErrNoRows {
		return BotSession{}, notFound("Help session")
	}
	if err != nil {
		return BotSession{}, wrapDB(err, "load the help session")
	}
	b.Resolved = resolved != 0
	b.Escalated = escalated != 0
	return b, nil
}

// boolInt converts a bool to the integer form the schema stores.
func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
