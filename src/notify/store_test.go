package notify

import (
	"context"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/config"
	"github.com/webappsgo/cashp/src/database"
	apperrors "github.com/webappsgo/cashp/src/errors"
)

// testClock is a manually advanced clock, so retention windows and retry
// backoff can be crossed in a test without sleeping.
type testClock struct {
	current time.Time
}

func (c *testClock) now() time.Time { return c.current }

func (c *testClock) advance(d time.Duration) { c.current = c.current.Add(d) }

// openTestStore opens a throwaway SQLite database, applies every registered
// schema fragment and returns a store driven by a controllable clock.
func openTestStore(t *testing.T) (*Store, *testClock) {
	t.Helper()

	clock := &testClock{current: time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)}
	db, err := database.Open(database.Config{Driver: database.DriverSQLite, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	store, err := NewStore(db, clock.now)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store, clock
}

// insertRecord stores one notification and returns its identifier.
func insertRecord(t *testing.T, store *Store, audience Audience, ownerID, title string, created time.Time) string {
	t.Helper()

	id, err := NewID()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	rec := Record{
		ID:        id,
		Audience:  audience,
		OwnerID:   ownerID,
		Event:     EventTest,
		Type:      TypeInfo,
		Surfaces:  SurfaceCenter,
		Title:     title,
		Body:      "body of " + title,
		Link:      "/notifications",
		CreatedAt: created,
	}
	if err := store.Insert(context.Background(), rec); err != nil {
		t.Fatalf("insert %s: %v", title, err)
	}
	return id
}

func TestNewStoreRequiresADatabase(t *testing.T) {
	if _, err := NewStore(nil, time.Now); err == nil {
		t.Fatal("a store without a database handle must not be created")
	}
}

func TestNewIDIsUniqueAndTimeOrdered(t *testing.T) {
	first, err := NewID()
	if err != nil {
		t.Fatalf("first id: %v", err)
	}
	second, err := NewID()
	if err != nil {
		t.Fatalf("second id: %v", err)
	}
	if first == second {
		t.Fatal("notification identifiers must be unique")
	}
	if first >= second {
		t.Fatalf("identifiers must sort by creation time, got %q then %q", first, second)
	}
}

func TestStoreListNewestFirstAndCountsUnread(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	insertRecord(t, store, AudienceUser, "user-1", "oldest", clock.now().Add(-2*time.Hour))
	middle := insertRecord(t, store, AudienceUser, "user-1", "middle", clock.now().Add(-time.Hour))
	insertRecord(t, store, AudienceUser, "user-1", "newest", clock.now())

	records, err := store.List(ctx, AudienceUser, "user-1", ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected three records, got %d", len(records))
	}
	if records[0].Title != "newest" || records[2].Title != "oldest" {
		t.Fatalf("records must come back newest first, got %q then %q", records[0].Title, records[2].Title)
	}
	if records[0].Audience != AudienceUser || records[0].OwnerID != "user-1" {
		t.Fatalf("the owner must round-trip, got %+v", records[0])
	}
	if records[0].Surfaces != SurfaceCenter || records[0].Type != TypeInfo {
		t.Fatalf("the surface and type must round-trip, got %+v", records[0])
	}

	count, err := store.UnreadCount(ctx, AudienceUser, "user-1")
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected three unread, got %d", count)
	}

	if err := store.MarkRead(ctx, AudienceUser, "user-1", middle); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	count, err = store.UnreadCount(ctx, AudienceUser, "user-1")
	if err != nil {
		t.Fatalf("unread count after read: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected two unread, got %d", count)
	}

	unread, err := store.List(ctx, AudienceUser, "user-1", ListOptions{UnreadOnly: true})
	if err != nil {
		t.Fatalf("list unread: %v", err)
	}
	if len(unread) != 2 {
		t.Fatalf("expected two unread rows, got %d", len(unread))
	}
	for _, rec := range unread {
		if rec.Title == "middle" {
			t.Fatal("a read notification must not appear in the unread list")
		}
	}
}

func TestStoreMarkReadIsScopedToTheOwner(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	mine := insertRecord(t, store, AudienceUser, "user-1", "mine", clock.now())
	insertRecord(t, store, AudienceUser, "user-2", "theirs", clock.now())

	if err := store.MarkRead(ctx, AudienceUser, "user-2", mine); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	count, err := store.UnreadCount(ctx, AudienceUser, "user-1")
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if count != 1 {
		t.Fatalf("another owner must not be able to read my notification, got %d unread", count)
	}
}

func TestStoreMarkAllReadAndDismissAll(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	for _, title := range []string{"one", "two", "three"} {
		insertRecord(t, store, AudienceAdmin, "admin-1", title, clock.now())
	}

	if err := store.MarkAllRead(ctx, AudienceAdmin, "admin-1"); err != nil {
		t.Fatalf("mark all read: %v", err)
	}
	count, err := store.UnreadCount(ctx, AudienceAdmin, "admin-1")
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected nothing unread, got %d", count)
	}

	if err := store.DismissAll(ctx, AudienceAdmin, "admin-1"); err != nil {
		t.Fatalf("dismiss all: %v", err)
	}
	visible, err := store.List(ctx, AudienceAdmin, "admin-1", ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(visible) != 0 {
		t.Fatalf("dismissed notifications must be hidden, got %d", len(visible))
	}

	all, err := store.List(ctx, AudienceAdmin, "admin-1", ListOptions{IncludeDismissed: true})
	if err != nil {
		t.Fatalf("list including dismissed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("a dismissal must be a soft delete, got %d rows", len(all))
	}
}

func TestStoreDismissHidesASingleRecord(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	first := insertRecord(t, store, AudienceUser, "user-1", "first", clock.now())
	insertRecord(t, store, AudienceUser, "user-1", "second", clock.now())

	if err := store.Dismiss(ctx, AudienceUser, "user-1", first); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	records, err := store.List(ctx, AudienceUser, "user-1", ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 1 || records[0].Title != "second" {
		t.Fatalf("only the dismissed record must disappear, got %d rows", len(records))
	}
}

func TestStoreKeepsAudiencesInSeparateTables(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	insertRecord(t, store, AudienceAdmin, "shared-id", "admin copy", clock.now())
	insertRecord(t, store, AudienceUser, "shared-id", "user copy", clock.now())

	admin, err := store.List(ctx, AudienceAdmin, "shared-id", ListOptions{})
	if err != nil {
		t.Fatalf("list admin: %v", err)
	}
	user, err := store.List(ctx, AudienceUser, "shared-id", ListOptions{})
	if err != nil {
		t.Fatalf("list user: %v", err)
	}
	if len(admin) != 1 || admin[0].Title != "admin copy" {
		t.Fatalf("unexpected admin rows %+v", admin)
	}
	if len(user) != 1 || user[0].Title != "user copy" {
		t.Fatalf("unexpected user rows %+v", user)
	}
	if AudienceAdmin.Table() == AudienceUser.Table() {
		t.Fatal("the two audiences must not share a table")
	}
}

func TestStoreListPaginates(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		insertRecord(t, store, AudienceUser, "user-1", "row", clock.now().Add(time.Duration(i)*time.Minute))
	}

	page, err := store.List(ctx, AudienceUser, "user-1", ListOptions{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("expected a page of two, got %d", len(page))
	}
}

func TestStorePruneDropsAgedRowsAndCapsPerOwner(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	old := clock.now().Add(-(RetentionDays + 1) * 24 * time.Hour)
	insertRecord(t, store, AudienceUser, "user-1", "expired", old)
	insertRecord(t, store, AudienceUser, "user-1", "fresh", clock.now())

	if err := store.Prune(ctx); err != nil {
		t.Fatalf("prune: %v", err)
	}
	records, err := store.List(ctx, AudienceUser, "user-1", ListOptions{IncludeDismissed: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 1 || records[0].Title != "fresh" {
		t.Fatalf("retention must drop only aged rows, got %+v", records)
	}

	for i := 0; i < MaxPerOwner+10; i++ {
		insertRecord(t, store, AudienceAdmin, "admin-1", "row", clock.now().Add(time.Duration(i)*time.Minute))
	}
	if err := store.Prune(ctx); err != nil {
		t.Fatalf("prune cap: %v", err)
	}
	capped, err := store.List(ctx, AudienceAdmin, "admin-1", ListOptions{Limit: MaxPerOwner, IncludeDismissed: true})
	if err != nil {
		t.Fatalf("list capped: %v", err)
	}
	if len(capped) > MaxPerOwner {
		t.Fatalf("no owner may keep more than %d records, got %d", MaxPerOwner, len(capped))
	}
}

func TestStoreDedupSuppressesARepeatInsideTheWindow(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	claimed, err := store.ClaimDedup(ctx, "backup:nightly", EventBackupFailed, time.Hour)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !claimed {
		t.Fatal("the first dispatch must win the key")
	}

	claimed, err = store.ClaimDedup(ctx, "backup:nightly", EventBackupFailed, time.Hour)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if claimed {
		t.Fatal("a repeat inside the window must be suppressed")
	}

	held, err := store.DedupHeld(ctx, "backup:nightly")
	if err != nil {
		t.Fatalf("held: %v", err)
	}
	if !held {
		t.Fatal("a live claim must be visible to the suppression rules")
	}

	clock.advance(2 * time.Hour)
	claimed, err = store.ClaimDedup(ctx, "backup:nightly", EventBackupFailed, time.Hour)
	if err != nil {
		t.Fatalf("claim after expiry: %v", err)
	}
	if !claimed {
		t.Fatal("an expired claim must be reclaimable")
	}
}

func TestStoreDedupIgnoresAnEmptyKey(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()

	claimed, err := store.ClaimDedup(ctx, "", EventTest, time.Hour)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !claimed {
		t.Fatal("a message without a dedup key must never be suppressed")
	}
	held, err := store.DedupHeld(ctx, "")
	if err != nil {
		t.Fatalf("held: %v", err)
	}
	if held {
		t.Fatal("an empty key can never be held")
	}
}

func TestStorePruneDedupClearsOnlyExpiredClaims(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	if _, err := store.ClaimDedup(ctx, "short", EventTest, time.Minute); err != nil {
		t.Fatalf("claim short: %v", err)
	}
	if _, err := store.ClaimDedup(ctx, "long", EventTest, 24*time.Hour); err != nil {
		t.Fatalf("claim long: %v", err)
	}

	clock.advance(time.Hour)
	if err := store.PruneDedup(ctx); err != nil {
		t.Fatalf("prune dedup: %v", err)
	}

	if held, _ := store.DedupHeld(ctx, "short"); held {
		t.Fatal("an expired claim must be pruned")
	}
	if held, _ := store.DedupHeld(ctx, "long"); !held {
		t.Fatal("a live claim must survive the prune")
	}
}

func TestStoreEnqueueIsIdempotent(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	delivery := Delivery{
		ID:             "delivery-1",
		NotificationID: "notification-1",
		Event:          EventTest,
		Channel:        ChannelSMTP,
		Recipient:      "ada@example.com",
		Payload:        "subject and body",
		NextAttemptAt:  clock.now(),
	}
	if err := store.Enqueue(ctx, delivery); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if err := store.Enqueue(ctx, delivery); err != nil {
		t.Fatalf("a duplicate enqueue must be a no-op, got %v", err)
	}

	due, err := store.Due(ctx, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("one identifier must produce exactly one delivery, got %d", len(due))
	}
	if due[0].Status != StatusPending || due[0].Channel != ChannelSMTP {
		t.Fatalf("unexpected delivery %+v", due[0])
	}
}

func TestStoreDueSkipsWorkScheduledForLater(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	later := Delivery{
		ID:            "later",
		Event:         EventTest,
		Channel:       ChannelSMTP,
		NextAttemptAt: clock.now().Add(time.Hour),
	}
	if err := store.Enqueue(ctx, later); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	due, err := store.Due(ctx, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("a future attempt must not be due yet, got %d", len(due))
	}

	clock.advance(2 * time.Hour)
	due, err = store.Due(ctx, 10)
	if err != nil {
		t.Fatalf("due after advance: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("the attempt must become due, got %d", len(due))
	}
}

func TestStoreRescheduleFollowsTheBackoffTableThenFails(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	if err := store.Enqueue(ctx, Delivery{ID: "d1", Event: EventTest, Channel: "generic", NextAttemptAt: clock.now()}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	cause := apperrors.New(apperrors.CodeUnavailable, 503, "receiver refused the delivery")

	for attempt := 1; attempt <= len(config.WebhookRetryBackoff); attempt++ {
		if err := store.Reschedule(ctx, "d1", attempt, cause); err != nil {
			t.Fatalf("reschedule %d: %v", attempt, err)
		}
		rows, err := store.Log(ctx, "generic", "", 10)
		if err != nil {
			t.Fatalf("log: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected one delivery row, got %d", len(rows))
		}
		if rows[0].Status != StatusPending {
			t.Fatalf("attempt %d must stay pending, got %s", attempt, rows[0].Status)
		}
		want := clock.now().Add(config.WebhookRetryBackoff[attempt-1]).Unix()
		if rows[0].NextAttemptAt.Unix() != want {
			t.Fatalf("attempt %d scheduled at %d, want %d", attempt, rows[0].NextAttemptAt.Unix(), want)
		}
		if rows[0].LastError == "" {
			t.Fatal("a failed attempt must record why it failed")
		}
	}

	exhausted := len(config.WebhookRetryBackoff) + 1
	if err := store.Reschedule(ctx, "d1", exhausted, cause); err != nil {
		t.Fatalf("final reschedule: %v", err)
	}
	rows, err := store.Log(ctx, "generic", "", 10)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("an exhausted delivery must never be deleted, got %d rows", len(rows))
	}
	if rows[0].Status != StatusFailed {
		t.Fatalf("expected the delivery to be marked failed, got %s", rows[0].Status)
	}
}

func TestStoreCompleteAndSuppressClearThePayload(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"sent", "stopped"} {
		if err := store.Enqueue(ctx, Delivery{ID: id, Event: EventTest, Channel: ChannelSMTP, Payload: "secret body", NextAttemptAt: clock.now()}); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
	}

	if err := store.Complete(ctx, "sent"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := store.Suppress(ctx, "stopped", "recipient opted out"); err != nil {
		t.Fatalf("suppress: %v", err)
	}

	rows, err := store.Log(ctx, ChannelSMTP, "", 10)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two rows, got %d", len(rows))
	}
	for _, row := range rows {
		if row.Payload != "" {
			t.Fatalf("a finished delivery must not keep its payload, got %q", row.Payload)
		}
		switch row.ID {
		case "sent":
			if row.Status != StatusSent {
				t.Fatalf("expected sent, got %s", row.Status)
			}
		case "stopped":
			if row.Status != StatusSuppressed || row.LastError == "" {
				t.Fatalf("a suppressed delivery must record its reason, got %+v", row)
			}
		}
	}

	if due, _ := store.Due(ctx, 10); len(due) != 0 {
		t.Fatalf("no finished delivery may still be due, got %d", len(due))
	}
}

func TestStoreLogFiltersByChannelAndStatus(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	deliveries := []Delivery{
		{ID: "a", Event: EventTest, Channel: ChannelSMTP, Status: StatusSent},
		{ID: "b", Event: EventTest, Channel: ChannelSMTP, Status: StatusFailed},
		{ID: "c", Event: EventTest, Channel: TransportSlack, Status: StatusSent},
	}
	for _, d := range deliveries {
		d.NextAttemptAt = clock.now()
		if err := store.Enqueue(ctx, d); err != nil {
			t.Fatalf("enqueue %s: %v", d.ID, err)
		}
	}

	rows, err := store.Log(ctx, ChannelSMTP, "", 10)
	if err != nil {
		t.Fatalf("log by channel: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two smtp rows, got %d", len(rows))
	}

	rows, err = store.Log(ctx, "", StatusSent, 10)
	if err != nil {
		t.Fatalf("log by status: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two sent rows, got %d", len(rows))
	}

	rows, err = store.Log(ctx, TransportSlack, StatusFailed, 10)
	if err != nil {
		t.Fatalf("log by both: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows, got %d", len(rows))
	}
}

func TestStoreMetricsGroupsCountsByChannel(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	deliveries := []Delivery{
		{ID: "a", Event: EventTest, Channel: ChannelSMTP, Status: StatusSent},
		{ID: "b", Event: EventTest, Channel: ChannelSMTP, Status: StatusFailed},
		{ID: "c", Event: EventTest, Channel: ChannelSMTP, Status: StatusPending},
		{ID: "d", Event: EventTest, Channel: ChannelSMTP, Status: StatusSuppressed},
		{ID: "e", Event: EventTest, Channel: TransportGotify, Status: StatusSent},
	}
	for _, d := range deliveries {
		if err := store.Enqueue(ctx, d); err != nil {
			t.Fatalf("enqueue %s: %v", d.ID, err)
		}
	}

	metrics, err := store.Metrics(ctx, clock.now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	byChannel := map[string]ChannelMetrics{}
	for _, m := range metrics {
		byChannel[m.Channel] = m
	}

	smtp, ok := byChannel[ChannelSMTP]
	if !ok {
		t.Fatal("the smtp channel must appear in the metrics")
	}
	if smtp.Sent != 1 || smtp.Failed != 1 || smtp.Pending != 1 || smtp.Suppressed != 1 {
		t.Fatalf("unexpected smtp metrics %+v", smtp)
	}
	if byChannel[TransportGotify].Sent != 1 {
		t.Fatalf("unexpected gotify metrics %+v", byChannel[TransportGotify])
	}

	future, err := store.Metrics(ctx, clock.now().Add(time.Hour))
	if err != nil {
		t.Fatalf("metrics window: %v", err)
	}
	if len(future) != 0 {
		t.Fatalf("nothing was created after the window, got %d rows", len(future))
	}
}

func TestStoreAuditTrailIsAppendOnlyAndNewestFirst(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	entries := []AuditEntry{
		{Actor: "admin", Action: ActionConfigChange, Channel: ChannelSMTP, Result: "saved", Detail: "host changed"},
		{Actor: "admin", Action: ActionChannelState, Channel: ChannelSMTP, Result: "active"},
		{Actor: "system", Action: ActionDeliver, Channel: TransportDiscord, Event: EventTest, Result: "sent"},
	}
	for i, entry := range entries {
		clock.advance(time.Duration(i+1) * time.Minute)
		if err := store.Audit(ctx, entry); err != nil {
			t.Fatalf("audit %d: %v", i, err)
		}
	}

	trail, err := store.AuditTrail(ctx, 10)
	if err != nil {
		t.Fatalf("audit trail: %v", err)
	}
	if len(trail) != 3 {
		t.Fatalf("every audited action must be kept, got %d", len(trail))
	}
	if trail[0].Action != ActionDeliver || trail[0].Channel != TransportDiscord {
		t.Fatalf("the newest entry must come first, got %+v", trail[0])
	}
	if trail[2].Action != ActionConfigChange || trail[2].Detail != "host changed" {
		t.Fatalf("the oldest entry must survive unchanged, got %+v", trail[2])
	}

	if err := store.Audit(ctx, AuditEntry{Actor: "admin", Action: ActionPreference, Result: "updated"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	trail, err = store.AuditTrail(ctx, 10)
	if err != nil {
		t.Fatalf("audit trail after append: %v", err)
	}
	if len(trail) != 4 {
		t.Fatalf("an append must never replace earlier rows, got %d", len(trail))
	}
}

func TestStoreEraseRemovesTheRecipientsData(t *testing.T) {
	store, clock := openTestStore(t)
	ctx := context.Background()

	insertRecord(t, store, AudienceUser, "user-1", "kept for now", clock.now())
	insertRecord(t, store, AudienceUser, "user-2", "somebody else", clock.now())
	if err := store.Enqueue(ctx, Delivery{ID: "d1", Event: EventTest, Channel: ChannelSMTP, Recipient: "ada@example.com", Payload: "body"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := store.Enqueue(ctx, Delivery{ID: "d2", Event: EventTest, Channel: ChannelSMTP, Recipient: "other@example.com", Payload: "body"}); err != nil {
		t.Fatalf("enqueue other: %v", err)
	}

	if err := store.Erase(ctx, AudienceUser, "user-1", "ada@example.com"); err != nil {
		t.Fatalf("erase: %v", err)
	}

	erased, err := store.List(ctx, AudienceUser, "user-1", ListOptions{IncludeDismissed: true})
	if err != nil {
		t.Fatalf("list erased: %v", err)
	}
	if len(erased) != 0 {
		t.Fatalf("an erasure must remove every notification, got %d", len(erased))
	}
	kept, err := store.List(ctx, AudienceUser, "user-2", ListOptions{})
	if err != nil {
		t.Fatalf("list kept: %v", err)
	}
	if len(kept) != 1 {
		t.Fatal("an erasure must not touch another owner")
	}

	rows, err := store.Log(ctx, ChannelSMTP, "", 10)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	for _, row := range rows {
		switch row.ID {
		case "d1":
			if row.Recipient != "" || row.Payload != "" {
				t.Fatalf("the erased address must be gone from the log, got %+v", row)
			}
		case "d2":
			if row.Recipient != "other@example.com" {
				t.Fatalf("another recipient must be untouched, got %+v", row)
			}
		}
	}

	trail, err := store.AuditTrail(ctx, 10)
	if err != nil {
		t.Fatalf("audit trail: %v", err)
	}
	if len(trail) != 1 || trail[0].Action != ActionErasure {
		t.Fatalf("an erasure must leave an audit record, got %+v", trail)
	}
	if trail[0].Detail == "" {
		t.Fatal("the audit record must say what happened")
	}
}
