package notify

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/config"
	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/errors"
)

// Delivery statuses recorded in notification_deliveries.
const (
	// StatusPending is a delivery waiting for its first or next attempt.
	StatusPending = "pending"
	// StatusSent is a delivery the channel accepted.
	StatusSent = "sent"
	// StatusFailed is a delivery that exhausted its retries and was moved
	// out of the queue. It is kept, never silently discarded.
	StatusFailed = "failed"
	// StatusSuppressed is a delivery that a preference, a dedup claim or an
	// event suppression rule stopped before it left the server.
	StatusSuppressed = "suppressed"
)

// Audit actions recorded in notification_audit.
const (
	// ActionDispatch records a dispatch decision.
	ActionDispatch = "dispatch"
	// ActionDeliver records one delivery attempt outcome.
	ActionDeliver = "deliver"
	// ActionChannelState records a channel lifecycle transition.
	ActionChannelState = "channel_state"
	// ActionConfigChange records a configuration or template change.
	ActionConfigChange = "config_change"
	// ActionPreference records a recipient preference change.
	ActionPreference = "preference"
	// ActionErasure records a right-to-erasure purge.
	ActionErasure = "erasure"
)

// Record is one stored WebUI notification.
type Record struct {
	// ID is the record identifier, shared with the delivery that created it.
	ID string
	// Audience selects which store the record lives in.
	Audience Audience
	// OwnerID is the admin or user the record belongs to.
	OwnerID string
	// Event is the catalog event name.
	Event string
	// Type is the notification type driving the WebUI styling.
	Type Type
	// Surfaces is the placement bitmask.
	Surfaces Surface
	// Title is the headline.
	Title string
	// Body is the message text.
	Body string
	// Link is the in-app deep link.
	Link string
	// ReadAt is when the recipient read it, zero while unread.
	ReadAt time.Time
	// DismissedAt is when the recipient dismissed it, zero while visible.
	DismissedAt time.Time
	// CreatedAt is when the record was stored.
	CreatedAt time.Time
}

// ListOptions filters a notification listing.
type ListOptions struct {
	// UnreadOnly restricts the result to unread records.
	UnreadOnly bool
	// IncludeDismissed keeps dismissed records in the result.
	IncludeDismissed bool
	// Limit caps the number of rows. Zero applies MaxPerOwner.
	Limit int
	// Offset skips rows for pagination.
	Offset int
}

// Delivery is one queued or completed outbound delivery attempt.
type Delivery struct {
	// ID is the idempotency key, stable across every retry.
	ID string
	// NotificationID links the delivery to its stored WebUI record.
	NotificationID string
	// Event is the catalog event name.
	Event string
	// Channel is the channel that owns the attempt.
	Channel string
	// Role is the contact role for webhook deliveries.
	Role string
	// Recipient is the email address, or empty for a webhook.
	Recipient string
	// Status is one of the Status* constants.
	Status string
	// Attempt is how many attempts have completed.
	Attempt int
	// NextAttemptAt is when the next retry becomes due.
	NextAttemptAt time.Time
	// LastError is the most recent failure message.
	LastError string
	// Payload is the serialised Rendered value used to retry.
	Payload string
	// CreatedAt is when the delivery was queued.
	CreatedAt time.Time
	// UpdatedAt is when the delivery last changed.
	UpdatedAt time.Time
}

// AuditEntry is one append-only audit record.
type AuditEntry struct {
	// Actor is the admin, user or subsystem responsible.
	Actor string
	// Action is one of the Action* constants.
	Action string
	// Channel is the channel involved, when there is one.
	Channel string
	// Event is the catalog event involved, when there is one.
	Event string
	// Result is the outcome, for example "sent" or "blocked".
	Result string
	// Detail is free-form context. It must never contain a secret.
	Detail string
}

// ChannelMetrics is the aggregate delivery outcome for one channel, used by
// the admin panel's delivery metrics view.
type ChannelMetrics struct {
	// Channel is the channel name.
	Channel string
	// Sent is the number of accepted deliveries.
	Sent int64
	// Failed is the number that exhausted their retries.
	Failed int64
	// Pending is the number still queued.
	Pending int64
	// Suppressed is the number stopped before leaving the server.
	Suppressed int64
}

// Store is the persistence layer for notifications, deliveries, dedup
// claims and the audit trail. Every query names its columns explicitly and
// uses the timeout-taking database helpers.
type Store struct {
	db  *database.DB
	now func() time.Time
}

// NewStore returns a store over an open database handle.
func NewStore(db *database.DB, now func() time.Time) (*Store, error) {
	if db == nil {
		return nil, errors.New(errors.CodeInternal, http.StatusInternalServerError, "notification store needs a database handle")
	}
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, now: now}, nil
}

// NewID returns a time-ordered identifier for a notification or delivery.
func NewID() (string, error) {
	id, err := config.NewWebhookID()
	if err != nil {
		return "", errors.Wrap(err, errors.CodeInternal, http.StatusInternalServerError, "generate notification id")
	}
	return id, nil
}

// Insert stores one WebUI notification.
func (s *Store) Insert(ctx context.Context, rec Record) error {
	table := rec.Audience.Table()
	owner := ownerColumn(rec.Audience)

	query := fmt.Sprintf(
		"INSERT INTO %s (id, %s, event, type, surfaces, title, body, link, read_at, dismissed_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		table, owner,
	)
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, query,
		rec.ID, rec.OwnerID, rec.Event, string(rec.Type), int64(rec.Surfaces),
		rec.Title, rec.Body, rec.Link, unixOrZero(rec.ReadAt), unixOrZero(rec.DismissedAt), unixOrZero(rec.CreatedAt),
	)
	if err != nil {
		return database.Classify(err)
	}
	return nil
}

// List returns one owner's notifications, newest first.
func (s *Store) List(ctx context.Context, audience Audience, ownerID string, opts ListOptions) ([]Record, error) {
	table := audience.Table()
	owner := ownerColumn(audience)

	query := fmt.Sprintf(
		"SELECT id, %s, event, type, surfaces, title, body, link, read_at, dismissed_at, created_at FROM %s WHERE %s = ?",
		owner, table, owner,
	)
	if opts.UnreadOnly {
		query += " AND read_at = 0"
	}
	if !opts.IncludeDismissed {
		query += " AND dismissed_at = 0"
	}
	limit := opts.Limit
	if limit <= 0 || limit > MaxPerOwner {
		limit = MaxPerOwner
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?"

	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query, ownerID, limit, maxInt(opts.Offset, 0))
	if err != nil {
		return nil, database.Classify(err)
	}
	defer func() { _ = rows.Close() }()

	var out []Record
	for rows.Next() {
		rec, err := scanRecord(rows, audience)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, database.Classify(err)
	}
	return out, nil
}

// UnreadCount returns the badge count for one owner.
func (s *Store) UnreadCount(ctx context.Context, audience Audience, ownerID string) (int64, error) {
	query := fmt.Sprintf(
		"SELECT COUNT(*) FROM %s WHERE %s = ? AND read_at = 0 AND dismissed_at = 0",
		audience.Table(), ownerColumn(audience),
	)
	var count int64
	if err := s.db.QueryRowContext(ctx, database.TimeoutSelect, query, ownerID).Scan(&count); err != nil {
		return 0, database.Classify(err)
	}
	return count, nil
}

// MarkRead marks one notification read. Marking an already read record is
// not an error and does not move its timestamp.
func (s *Store) MarkRead(ctx context.Context, audience Audience, ownerID, id string) error {
	query := fmt.Sprintf(
		"UPDATE %s SET read_at = ? WHERE id = ? AND %s = ? AND read_at = 0",
		audience.Table(), ownerColumn(audience),
	)
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite, query, s.now().Unix(), id, ownerID); err != nil {
		return database.Classify(err)
	}
	return nil
}

// MarkAllRead marks every unread notification for one owner.
func (s *Store) MarkAllRead(ctx context.Context, audience Audience, ownerID string) error {
	query := fmt.Sprintf(
		"UPDATE %s SET read_at = ? WHERE %s = ? AND read_at = 0",
		audience.Table(), ownerColumn(audience),
	)
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite, query, s.now().Unix(), ownerID); err != nil {
		return database.Classify(err)
	}
	return nil
}

// Dismiss hides one notification without deleting it, so the audit trail
// and the retention sweep still see it.
func (s *Store) Dismiss(ctx context.Context, audience Audience, ownerID, id string) error {
	now := s.now().Unix()
	query := fmt.Sprintf(
		"UPDATE %s SET dismissed_at = ?, read_at = CASE WHEN read_at = 0 THEN ? ELSE read_at END WHERE id = ? AND %s = ? AND dismissed_at = 0",
		audience.Table(), ownerColumn(audience),
	)
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite, query, now, now, id, ownerID); err != nil {
		return database.Classify(err)
	}
	return nil
}

// DismissAll hides every visible notification for one owner.
func (s *Store) DismissAll(ctx context.Context, audience Audience, ownerID string) error {
	now := s.now().Unix()
	query := fmt.Sprintf(
		"UPDATE %s SET dismissed_at = ?, read_at = CASE WHEN read_at = 0 THEN ? ELSE read_at END WHERE %s = ? AND dismissed_at = 0",
		audience.Table(), ownerColumn(audience),
	)
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite, query, now, now, ownerID); err != nil {
		return database.Classify(err)
	}
	return nil
}

// Prune enforces the retention rules: nothing older than RetentionDays, and
// no more than MaxPerOwner records per admin or user.
func (s *Store) Prune(ctx context.Context) error {
	cutoff := s.now().Add(-RetentionDays * 24 * time.Hour).Unix()

	for _, audience := range []Audience{AudienceAdmin, AudienceUser} {
		table := audience.Table()
		owner := ownerColumn(audience)

		aged := fmt.Sprintf("DELETE FROM %s WHERE created_at < ?", table)
		if _, err := s.db.ExecContext(ctx, database.TimeoutBulk, aged, cutoff); err != nil {
			return database.Classify(err)
		}

		// Trimming to MaxPerOwner is done per owner because the portable
		// subset of SQL across the five supported drivers has no window
		// function this package can rely on.
		owners, err := s.owners(ctx, table, owner)
		if err != nil {
			return err
		}
		for _, ownerID := range owners {
			if err := s.trimOwner(ctx, table, owner, ownerID); err != nil {
				return err
			}
		}
	}
	return nil
}

// owners lists the distinct owners holding more than MaxPerOwner records.
func (s *Store) owners(ctx context.Context, table, owner string) ([]string, error) {
	query := fmt.Sprintf("SELECT %s FROM %s GROUP BY %s HAVING COUNT(*) > ?", owner, table, owner)
	rows, err := s.db.QueryContext(ctx, database.TimeoutBulk, query, MaxPerOwner)
	if err != nil {
		return nil, database.Classify(err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, database.Classify(err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, database.Classify(err)
	}
	return out, nil
}

// trimOwner deletes everything past the newest MaxPerOwner records for one
// owner.
func (s *Store) trimOwner(ctx context.Context, table, owner, ownerID string) error {
	query := fmt.Sprintf(
		"SELECT created_at FROM %s WHERE %s = ? ORDER BY created_at DESC, id DESC LIMIT 1 OFFSET ?",
		table, owner,
	)
	var cutoff int64
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect, query, ownerID, MaxPerOwner).Scan(&cutoff)
	if database.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return database.Classify(err)
	}

	del := fmt.Sprintf("DELETE FROM %s WHERE %s = ? AND created_at < ?", table, owner)
	if _, err := s.db.ExecContext(ctx, database.TimeoutBulk, del, ownerID, cutoff); err != nil {
		return database.Classify(err)
	}
	return nil
}

// ClaimDedup atomically claims a deduplication key. It returns false when
// the key is already held by a live claim, which is the signal to suppress
// the dispatch.
func (s *Store) ClaimDedup(ctx context.Context, key, event string, window time.Duration) (bool, error) {
	if key == "" {
		return true, nil
	}
	if window <= 0 {
		window = DefaultDedupWindow
	}
	now := s.now()

	// An expired claim is cleared first so the insert below can take the
	// key. Both statements run in one transaction so two nodes racing for
	// the same key cannot both win.
	claimed := false
	err := s.db.Tx(ctx, database.TimeoutWrite, func(tx *sql.Tx) error {
		expire := "DELETE FROM " + TableDedup + " WHERE dedup_key = ? AND expires_at <= ?"
		if _, err := tx.ExecContext(ctx, s.db.Rebind(expire), key, now.Unix()); err != nil {
			return err
		}

		insert := "INSERT INTO " + TableDedup + " (dedup_key, event, claimed_at, expires_at) VALUES (?, ?, ?, ?)"
		if _, err := tx.ExecContext(ctx, s.db.Rebind(insert), key, event, now.Unix(), now.Add(window).Unix()); err != nil {
			// A live claim already holds the key, so this dispatch is the
			// duplicate and must be suppressed rather than fail.
			if isDuplicateRow(err) {
				return nil
			}
			return err
		}
		claimed = true
		return nil
	})
	if err != nil {
		return false, database.Classify(err)
	}
	return claimed, nil
}

// DedupHeld reports whether a live claim holds the key, without taking it.
// The suppression rules need to read a marker another dispatch wrote, which
// claiming would overwrite.
func (s *Store) DedupHeld(ctx context.Context, key string) (bool, error) {
	if key == "" {
		return false, nil
	}
	query := "SELECT COUNT(*) FROM " + TableDedup + " WHERE dedup_key = ? AND expires_at > ?"
	var count int64
	if err := s.db.QueryRowContext(ctx, database.TimeoutSelect, query, key, s.now().Unix()).Scan(&count); err != nil {
		return false, database.Classify(err)
	}
	return count > 0, nil
}

// PruneDedup removes expired deduplication claims.
func (s *Store) PruneDedup(ctx context.Context) error {
	query := "DELETE FROM " + TableDedup + " WHERE expires_at <= ?"
	if _, err := s.db.ExecContext(ctx, database.TimeoutBulk, query, s.now().Unix()); err != nil {
		return database.Classify(err)
	}
	return nil
}

// Enqueue records a delivery in the queue. The identifier is the caller's
// idempotency key: re-enqueueing the same identifier is a no-op, which is
// what makes a worker that processes a message twice deliver it once.
func (s *Store) Enqueue(ctx context.Context, d Delivery) error {
	now := s.now().Unix()
	query := "INSERT INTO " + TableDeliveries + " (id, notification_id, event, channel, role, recipient, status, attempt, next_attempt_at, last_error, payload, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	_, err := s.db.ExecContext(ctx, database.TimeoutWrite, query,
		d.ID, d.NotificationID, d.Event, d.Channel, d.Role, d.Recipient,
		defaultString(d.Status, StatusPending), d.Attempt, unixOrZero(d.NextAttemptAt), d.LastError, d.Payload, now, now,
	)
	if err != nil {
		if isDuplicateRow(err) {
			return nil
		}
		return database.Classify(err)
	}
	return nil
}

// Due returns pending deliveries whose next attempt time has arrived.
func (s *Store) Due(ctx context.Context, limit int) ([]Delivery, error) {
	if limit <= 0 {
		limit = 50
	}
	query := "SELECT id, notification_id, event, channel, role, recipient, status, attempt, next_attempt_at, last_error, payload, created_at, updated_at FROM " +
		TableDeliveries + " WHERE status = ? AND next_attempt_at <= ? ORDER BY next_attempt_at ASC, id ASC LIMIT ?"

	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query, StatusPending, s.now().Unix(), limit)
	if err != nil {
		return nil, database.Classify(err)
	}
	defer func() { _ = rows.Close() }()

	var out []Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, database.Classify(err)
	}
	return out, nil
}

// Complete marks a delivery as accepted by its channel.
func (s *Store) Complete(ctx context.Context, id string) error {
	query := "UPDATE " + TableDeliveries + " SET status = ?, last_error = '', payload = '', updated_at = ? WHERE id = ?"
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite, query, StatusSent, s.now().Unix(), id); err != nil {
		return database.Classify(err)
	}
	return nil
}

// Reschedule records a failed attempt and sets the next retry time from the
// shared backoff table. When the attempts are exhausted the delivery is
// marked failed rather than deleted, so an operator can still see it.
func (s *Store) Reschedule(ctx context.Context, id string, attempt int, cause error) error {
	message := ""
	if cause != nil {
		message = truncate(errors.From(cause).Message, 500)
	}

	delay, more := config.WebhookRetryDelay(attempt)
	status := StatusPending
	next := s.now().Add(delay)
	if !more {
		status = StatusFailed
		next = time.Time{}
	}

	query := "UPDATE " + TableDeliveries + " SET status = ?, attempt = ?, next_attempt_at = ?, last_error = ?, updated_at = ? WHERE id = ?"
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite, query, status, attempt, unixOrZero(next), message, s.now().Unix(), id); err != nil {
		return database.Classify(err)
	}
	return nil
}

// Suppress records a delivery that was stopped before it left the server.
func (s *Store) Suppress(ctx context.Context, id, reason string) error {
	query := "UPDATE " + TableDeliveries + " SET status = ?, last_error = ?, payload = '', updated_at = ? WHERE id = ?"
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite, query, StatusSuppressed, truncate(reason, 500), s.now().Unix(), id); err != nil {
		return database.Classify(err)
	}
	return nil
}

// Metrics returns per-channel delivery counts for the admin dashboard.
func (s *Store) Metrics(ctx context.Context, since time.Time) ([]ChannelMetrics, error) {
	query := "SELECT channel, status, COUNT(*) FROM " + TableDeliveries + " WHERE created_at >= ? GROUP BY channel, status ORDER BY channel ASC"
	rows, err := s.db.QueryContext(ctx, database.TimeoutReport, query, unixOrZero(since))
	if err != nil {
		return nil, database.Classify(err)
	}
	defer func() { _ = rows.Close() }()

	index := map[string]*ChannelMetrics{}
	var order []string
	for rows.Next() {
		var channel, status string
		var count int64
		if err := rows.Scan(&channel, &status, &count); err != nil {
			return nil, database.Classify(err)
		}
		metrics, ok := index[channel]
		if !ok {
			metrics = &ChannelMetrics{Channel: channel}
			index[channel] = metrics
			order = append(order, channel)
		}
		switch status {
		case StatusSent:
			metrics.Sent += count
		case StatusFailed:
			metrics.Failed += count
		case StatusSuppressed:
			metrics.Suppressed += count
		default:
			metrics.Pending += count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, database.Classify(err)
	}

	out := make([]ChannelMetrics, 0, len(order))
	for _, channel := range order {
		out = append(out, *index[channel])
	}
	return out, nil
}

// Log returns recent deliveries for the admin notification log, optionally
// filtered by channel and status.
func (s *Store) Log(ctx context.Context, channel, status string, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := "SELECT id, notification_id, event, channel, role, recipient, status, attempt, next_attempt_at, last_error, payload, created_at, updated_at FROM " + TableDeliveries + " WHERE 1 = 1"
	args := []any{}
	if channel != "" {
		query += " AND channel = ?"
		args = append(args, channel)
	}
	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query, args...)
	if err != nil {
		return nil, database.Classify(err)
	}
	defer func() { _ = rows.Close() }()

	var out []Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, database.Classify(err)
	}
	return out, nil
}

// Audit appends one immutable audit record. Audit rows are never updated
// or deleted by this package.
func (s *Store) Audit(ctx context.Context, entry AuditEntry) error {
	id, err := NewID()
	if err != nil {
		return err
	}
	query := "INSERT INTO " + TableAudit + " (id, occurred_at, actor, action, channel, event, result, detail) VALUES (?, ?, ?, ?, ?, ?, ?, ?)"
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite, query,
		id, s.now().Unix(), entry.Actor, entry.Action, entry.Channel, entry.Event, entry.Result, truncate(entry.Detail, 2000),
	); err != nil {
		return database.Classify(err)
	}
	return nil
}

// AuditTrail returns the most recent audit records.
func (s *Store) AuditTrail(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := "SELECT actor, action, channel, event, result, detail FROM " + TableAudit + " ORDER BY occurred_at DESC, id DESC LIMIT ?"
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query, limit)
	if err != nil {
		return nil, database.Classify(err)
	}
	defer func() { _ = rows.Close() }()

	var out []AuditEntry
	for rows.Next() {
		var entry AuditEntry
		var detail sql.NullString
		if err := rows.Scan(&entry.Actor, &entry.Action, &entry.Channel, &entry.Event, &entry.Result, &detail); err != nil {
			return nil, database.Classify(err)
		}
		entry.Detail = detail.String
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, database.Classify(err)
	}
	return out, nil
}

// Erase removes one recipient's notifications, preferences and delivery
// payloads, satisfying a right-to-erasure request. The audit trail keeps
// the fact that an erasure happened but not the erased content.
func (s *Store) Erase(ctx context.Context, audience Audience, ownerID, email string) error {
	table := audience.Table()
	owner := ownerColumn(audience)

	if _, err := s.db.ExecContext(ctx, database.TimeoutBulk,
		fmt.Sprintf("DELETE FROM %s WHERE %s = ?", table, owner), ownerID); err != nil {
		return database.Classify(err)
	}
	if _, err := s.db.ExecContext(ctx, database.TimeoutBulk,
		"DELETE FROM "+TablePreferences+" WHERE audience = ? AND owner_id = ?", string(audience), ownerID); err != nil {
		return database.Classify(err)
	}
	if email != "" {
		if _, err := s.db.ExecContext(ctx, database.TimeoutBulk,
			"UPDATE "+TableDeliveries+" SET recipient = '', payload = '', updated_at = ? WHERE recipient = ?",
			s.now().Unix(), email); err != nil {
			return database.Classify(err)
		}
	}
	return s.Audit(ctx, AuditEntry{Actor: ownerID, Action: ActionErasure, Result: "purged", Detail: "notification history erased on request"})
}

// scanRecord reads one notification row.
func scanRecord(rows *sql.Rows, audience Audience) (Record, error) {
	var (
		rec      Record
		kind     string
		surfaces int64
		body     sql.NullString
		read     int64
		dismiss  int64
		created  int64
	)
	if err := rows.Scan(&rec.ID, &rec.OwnerID, &rec.Event, &kind, &surfaces, &rec.Title, &body, &rec.Link, &read, &dismiss, &created); err != nil {
		return Record{}, database.Classify(err)
	}
	rec.Audience = audience
	rec.Type = Type(kind)
	rec.Surfaces = Surface(surfaces)
	rec.Body = body.String
	rec.ReadAt = timeOrZero(read)
	rec.DismissedAt = timeOrZero(dismiss)
	rec.CreatedAt = timeOrZero(created)
	return rec, nil
}

// scanDelivery reads one delivery row.
func scanDelivery(rows *sql.Rows) (Delivery, error) {
	var (
		d       Delivery
		next    int64
		payload sql.NullString
		created int64
		updated int64
	)
	if err := rows.Scan(&d.ID, &d.NotificationID, &d.Event, &d.Channel, &d.Role, &d.Recipient,
		&d.Status, &d.Attempt, &next, &d.LastError, &payload, &created, &updated); err != nil {
		return Delivery{}, database.Classify(err)
	}
	d.NextAttemptAt = timeOrZero(next)
	d.Payload = payload.String
	d.CreatedAt = timeOrZero(created)
	d.UpdatedAt = timeOrZero(updated)
	return d, nil
}

// ownerColumn returns the owner column name for an audience. The value is
// never caller-supplied, so interpolating it into a statement is safe.
func ownerColumn(audience Audience) string {
	if audience == AudienceAdmin {
		return "admin_id"
	}
	return "user_id"
}

// unixOrZero renders a time as Unix seconds, mapping the zero time to 0.
func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// timeOrZero is the inverse of unixOrZero.
func timeOrZero(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

// defaultString returns value, or fallback when value is empty.
func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// truncate caps a stored string so a verbose upstream error cannot bloat a
// row.
func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

// isDuplicateRow reports whether an INSERT failed because the primary key is
// already taken, across every supported driver. The database package keeps
// an equivalent helper for its own use, but it is not exported.
func isDuplicateRow(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	needles := []string{
		"unique constraint",
		"duplicate entry",
		"duplicate key",
		"violation of primary key",
	}
	for _, needle := range needles {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}

// maxInt returns the larger of two integers.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
