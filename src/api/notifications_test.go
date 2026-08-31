package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/database"
	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/notify"
)

// openTestNotifyStore builds an in-memory-backed notify.Store the same way
// src/notify/store_test.go does, so the fixture stays representative of the
// real schema.
func openTestNotifyStore(t *testing.T) *notify.Store {
	t.Helper()
	db, err := database.Open(database.Config{Driver: database.DriverSQLite, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	store, err := notify.NewStore(db, time.Now)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// fixedCaller returns a CallerFunc that always resolves to the given
// audience/owner, standing in for the real session/token extraction that
// belongs to the eventual composition root.
func fixedCaller(audience notify.Audience, ownerID string) CallerFunc {
	return func(r *http.Request) (notify.Audience, string, error) {
		return audience, ownerID, nil
	}
}

// rejectingCaller always reports an unauthenticated request.
func rejectingCaller(r *http.Request) (notify.Audience, string, error) {
	return "", "", apperr.New(apperr.CodeUnauthorized, 401, "no session")
}

func newNotificationsHandler(t *testing.T, store *notify.Store, caller CallerFunc) *Notifications {
	t.Helper()
	n, err := NewNotifications(NotificationsOptions{Store: store, Caller: caller})
	if err != nil {
		t.Fatalf("NewNotifications: %v", err)
	}
	return n
}

func seedNotification(t *testing.T, store *notify.Store, audience notify.Audience, owner, id, event string) {
	t.Helper()
	if err := store.Insert(context.Background(), notify.Record{
		ID: id, Audience: audience, OwnerID: owner, Event: event,
		Type: notify.TypeInfo, Surfaces: notify.SurfaceCenter, Title: "Test event",
	}); err != nil {
		t.Fatalf("seed Insert: %v", err)
	}
}

func decodeJSON(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", body, err)
	}
}

func TestNotificationsListRejectsUnauthenticated(t *testing.T) {
	store := openTestNotifyStore(t)
	n := newNotificationsHandler(t, store, rejectingCaller)

	w := httptest.NewRecorder()
	r := newRequest("GET", APIPath("notifications"), "application/json")
	n.List(w, r)

	if w.Code != 401 {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	var failure Failure
	decodeJSON(t, w.Body.Bytes(), &failure)
	if failure.OK {
		t.Fatalf("expected ok=false, got %+v", failure)
	}
	if failure.Error != apperr.CodeUnauthorized {
		t.Fatalf("error = %q, want %q", failure.Error, apperr.CodeUnauthorized)
	}
}

func TestNotificationsListReturnsPagedEnvelope(t *testing.T) {
	store := openTestNotifyStore(t)
	seedNotification(t, store, notify.AudienceAdmin, "admin-1", "note-1", notify.EventBackupComplete)
	seedNotification(t, store, notify.AudienceAdmin, "admin-1", "note-2", notify.EventSSLExpiring)
	// A different owner's notification must never leak into the list.
	seedNotification(t, store, notify.AudienceAdmin, "admin-2", "note-3", notify.EventBackupComplete)

	n := newNotificationsHandler(t, store, fixedCaller(notify.AudienceAdmin, "admin-1"))

	w := httptest.NewRecorder()
	r := newRequest("GET", APIPath("notifications"), "application/json")
	n.List(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var page PageResponse
	decodeJSON(t, w.Body.Bytes(), &page)
	items, ok := page.Data.([]any)
	if !ok {
		t.Fatalf("data is not a list: %#v", page.Data)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if page.Pagination.Page != 1 || page.Pagination.Limit != DefaultPageSize {
		t.Fatalf("pagination = %+v, want page 1 / default limit", page.Pagination)
	}
}

func TestNotificationsListUnreadFilter(t *testing.T) {
	store := openTestNotifyStore(t)
	seedNotification(t, store, notify.AudienceAdmin, "admin-1", "note-1", notify.EventBackupComplete)
	seedNotification(t, store, notify.AudienceAdmin, "admin-1", "note-2", notify.EventSSLExpiring)
	if err := store.MarkRead(context.Background(), notify.AudienceAdmin, "admin-1", "note-1"); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	n := newNotificationsHandler(t, store, fixedCaller(notify.AudienceAdmin, "admin-1"))

	w := httptest.NewRecorder()
	r := newRequest("GET", APIPath("notifications")+"?unread=true", "application/json")
	n.List(w, r)

	var page PageResponse
	decodeJSON(t, w.Body.Bytes(), &page)
	items, _ := page.Data.([]any)
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1 unread-only", len(items))
	}
}

func TestNotificationsListEveryAcceptFormat(t *testing.T) {
	store := openTestNotifyStore(t)
	seedNotification(t, store, notify.AudienceAdmin, "admin-1", "note-1", notify.EventBackupComplete)
	n := newNotificationsHandler(t, store, fixedCaller(notify.AudienceAdmin, "admin-1"))

	cases := []struct {
		accept      string
		wantContain string
	}{
		{"application/json", `"ok"`},
		{"text/plain", "id:"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		r := newRequest("GET", APIPath("notifications"), tc.accept)
		n.List(w, r)
		if w.Code != 200 {
			t.Fatalf("accept=%s status = %d body=%s", tc.accept, w.Code, w.Body.String())
		}
	}

	// The .txt suffix must also negotiate to plain text regardless of Accept.
	w := httptest.NewRecorder()
	r := newRequest("GET", APIPath("notifications")+".txt", "application/json")
	n.List(w, r)
	if w.Code != 200 {
		t.Fatalf(".txt status = %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct == "" {
		t.Fatal("expected a Content-Type header")
	}
}

func TestNotificationsCountBareEnvelope(t *testing.T) {
	store := openTestNotifyStore(t)
	seedNotification(t, store, notify.AudienceAdmin, "admin-1", "note-1", notify.EventBackupComplete)
	seedNotification(t, store, notify.AudienceAdmin, "admin-1", "note-2", notify.EventSSLExpiring)
	n := newNotificationsHandler(t, store, fixedCaller(notify.AudienceAdmin, "admin-1"))

	w := httptest.NewRecorder()
	r := newRequest("GET", APIPath("notifications", "count"), "application/json")
	n.Count(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Unread int64 `json:"unread"`
	}
	decodeJSON(t, w.Body.Bytes(), &body)
	if body.Unread != 2 {
		t.Fatalf("unread = %d, want 2", body.Unread)
	}
	// Bare means no "ok"/"data" envelope wrapper at all.
	var raw map[string]any
	decodeJSON(t, w.Body.Bytes(), &raw)
	if _, has := raw["ok"]; has {
		t.Fatalf("count response must be bare, got %s", w.Body.String())
	}
}

func TestNotificationsCountRejectsUnauthenticated(t *testing.T) {
	store := openTestNotifyStore(t)
	n := newNotificationsHandler(t, store, rejectingCaller)

	w := httptest.NewRecorder()
	r := newRequest("GET", APIPath("notifications", "count"), "application/json")
	n.Count(w, r)

	if w.Code != 401 {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestNotificationsMarkReadSuccessEnvelope(t *testing.T) {
	store := openTestNotifyStore(t)
	seedNotification(t, store, notify.AudienceAdmin, "admin-1", "note-1", notify.EventBackupComplete)
	n := newNotificationsHandler(t, store, fixedCaller(notify.AudienceAdmin, "admin-1"))

	w := httptest.NewRecorder()
	r := newRequest("POST", APIPath("notifications", "note-1", "read"), "application/json")
	r.SetPathValue("id", "note-1")
	n.MarkRead(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var success Success
	decodeJSON(t, w.Body.Bytes(), &success)
	if !success.OK {
		t.Fatalf("expected ok=true, got %s", w.Body.String())
	}

	unread, err := store.UnreadCount(context.Background(), notify.AudienceAdmin, "admin-1")
	if err != nil {
		t.Fatalf("UnreadCount: %v", err)
	}
	if unread != 0 {
		t.Fatalf("unread = %d, want 0 after mark-read", unread)
	}
}

func TestNotificationsMarkReadMissingID(t *testing.T) {
	store := openTestNotifyStore(t)
	n := newNotificationsHandler(t, store, fixedCaller(notify.AudienceAdmin, "admin-1"))

	w := httptest.NewRecorder()
	r := newRequest("POST", APIPath("notifications", "", "read"), "application/json")
	n.MarkRead(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestNotificationsMarkAllReadSuccessEnvelope(t *testing.T) {
	store := openTestNotifyStore(t)
	seedNotification(t, store, notify.AudienceAdmin, "admin-1", "note-1", notify.EventBackupComplete)
	seedNotification(t, store, notify.AudienceAdmin, "admin-1", "note-2", notify.EventSSLExpiring)
	n := newNotificationsHandler(t, store, fixedCaller(notify.AudienceAdmin, "admin-1"))

	w := httptest.NewRecorder()
	r := newRequest("POST", APIPath("notifications", "read-all"), "application/json")
	n.MarkAllRead(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	unread, err := store.UnreadCount(context.Background(), notify.AudienceAdmin, "admin-1")
	if err != nil {
		t.Fatalf("UnreadCount: %v", err)
	}
	if unread != 0 {
		t.Fatalf("unread = %d, want 0 after mark-all-read", unread)
	}
}

func TestNotificationsMarkAllReadRejectsUnauthenticated(t *testing.T) {
	store := openTestNotifyStore(t)
	n := newNotificationsHandler(t, store, rejectingCaller)

	w := httptest.NewRecorder()
	r := newRequest("POST", APIPath("notifications", "read-all"), "application/json")
	n.MarkAllRead(w, r)

	if w.Code != 401 {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestNotificationsDeleteDismissesAndSucceeds(t *testing.T) {
	store := openTestNotifyStore(t)
	seedNotification(t, store, notify.AudienceAdmin, "admin-1", "note-1", notify.EventBackupComplete)
	n := newNotificationsHandler(t, store, fixedCaller(notify.AudienceAdmin, "admin-1"))

	w := httptest.NewRecorder()
	r := newRequest("DELETE", APIPath("notifications", "note-1"), "application/json")
	r.SetPathValue("id", "note-1")
	n.Delete(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	records, err := store.List(context.Background(), notify.AudienceAdmin, "admin-1", notify.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected the dismissed record to drop out of the default listing, got %d", len(records))
	}
}

func TestNotificationsDeleteRejectsUnauthenticated(t *testing.T) {
	store := openTestNotifyStore(t)
	n := newNotificationsHandler(t, store, rejectingCaller)

	w := httptest.NewRecorder()
	r := newRequest("DELETE", APIPath("notifications", "note-1"), "application/json")
	r.SetPathValue("id", "note-1")
	n.Delete(w, r)

	if w.Code != 401 {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestNotificationsWrongMethodRejected(t *testing.T) {
	store := openTestNotifyStore(t)
	n := newNotificationsHandler(t, store, fixedCaller(notify.AudienceAdmin, "admin-1"))

	w := httptest.NewRecorder()
	r := newRequest("DELETE", APIPath("notifications"), "application/json")
	n.List(w, r)

	if w.Code != 405 {
		t.Fatalf("status = %d, want 405", w.Code)
	}
	if allow := w.Header().Get("Allow"); allow != "GET" {
		t.Fatalf("Allow header = %q, want GET", allow)
	}
}

func TestNotificationsRoutesCoverCanonicalAndAlias(t *testing.T) {
	store := openTestNotifyStore(t)
	n := newNotificationsHandler(t, store, fixedCaller(notify.AudienceAdmin, "admin-1"))

	routes := n.Routes()
	if len(routes) != 10 {
		t.Fatalf("len(Routes()) = %d, want 10 (5 canonical + 5 alias)", len(routes))
	}

	canonical := map[string]bool{}
	aliases := map[string]bool{}
	for _, rt := range routes {
		if rt.Handler == nil {
			t.Fatalf("route %s has a nil handler", rt.Name)
		}
		if rt.Alias {
			aliases[rt.Name] = true
			if rt.Canonical == "" {
				t.Fatalf("alias route %s has no Canonical pattern", rt.Name)
			}
			continue
		}
		canonical[rt.Name] = true
		if !rt.Auth {
			t.Fatalf("route %s must require auth", rt.Name)
		}
	}
	if len(canonical) != 5 || len(aliases) != 5 {
		t.Fatalf("canonical=%d alias=%d, want 5/5", len(canonical), len(aliases))
	}
}

func TestNewNotificationsRequiresDependencies(t *testing.T) {
	if _, err := NewNotifications(NotificationsOptions{}); err == nil {
		t.Fatal("expected an error with no store and no caller")
	}
	store := openTestNotifyStore(t)
	if _, err := NewNotifications(NotificationsOptions{Store: store}); err == nil {
		t.Fatal("expected an error with no caller")
	}
}
