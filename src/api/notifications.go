package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/notify"
)

// CallerFunc resolves the audience and owner identifier of an authenticated
// request. src/auth does not build yet (a pre-existing, unrelated failure
// tracked separately), so this package cannot depend on its session/token
// types. A caller of NewNotifications supplies its own extraction function
// instead - typically a thin adapter over whatever session/token mechanism
// the composition root ends up using - and this handler never assumes a
// particular one. Returning a non-nil error means "unauthenticated" and is
// always rendered as 401 UNAUTHORIZED regardless of the underlying error.
type CallerFunc func(r *http.Request) (audience notify.Audience, ownerID string, err error)

// NotificationsOptions configures the notifications endpoint group.
type NotificationsOptions struct {
	// Store is the notification store notifications are read from and
	// written to. Required.
	Store *notify.Store
	// Caller resolves the authenticated caller's audience and owner id.
	// Required.
	Caller CallerFunc
}

// Notifications serves the per-caller notification center over HTTP:
// list, unread count, mark-read, mark-all-read and dismiss. It holds no
// global state - every dependency arrives through NotificationsOptions -
// so nothing here can be mistaken for a singleton.
type Notifications struct {
	opts NotificationsOptions
}

// NewNotifications builds the notifications handler group.
func NewNotifications(opts NotificationsOptions) (*Notifications, error) {
	if opts.Store == nil {
		return nil, errors.New(errors.CodeInternal, http.StatusInternalServerError, "notifications endpoint needs a store")
	}
	if opts.Caller == nil {
		return nil, errors.New(errors.CodeInternal, http.StatusInternalServerError, "notifications endpoint needs a caller resolver")
	}
	return &Notifications{opts: opts}, nil
}

// errUnauthorized is the fixed envelope for every caller-resolution
// failure. The underlying reason is never disclosed to the client.
var errUnauthorized = errors.New(errors.CodeUnauthorized, http.StatusUnauthorized, "authentication is required")

// caller resolves the request's audience and owner id, writing the
// unauthorized envelope and reporting false when resolution fails.
func (n *Notifications) caller(w http.ResponseWriter, r *http.Request) (notify.Audience, string, bool) {
	audience, ownerID, err := n.opts.Caller(r)
	if err != nil || ownerID == "" {
		WriteError(w, r, errUnauthorized)
		return "", "", false
	}
	return audience, ownerID, true
}

// notificationView is the wire representation of one stored notification.
type notificationView struct {
	ID          string `json:"id"`
	Event       string `json:"event"`
	Type        string `json:"type"`
	Surfaces    string `json:"surfaces"`
	Title       string `json:"title"`
	Body        string `json:"body,omitempty"`
	Link        string `json:"link,omitempty"`
	Read        bool   `json:"read"`
	ReadAt      string `json:"read_at,omitempty"`
	Dismissed   bool   `json:"dismissed"`
	DismissedAt string `json:"dismissed_at,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// RenderText renders one notification for the dot-notation plain-text
// format, matching the convention TextOf uses for every other endpoint.
func (v notificationView) RenderText() string {
	var b strings.Builder
	b.WriteString("id: " + v.ID + "\n")
	b.WriteString("event: " + v.Event + "\n")
	b.WriteString("type: " + v.Type + "\n")
	b.WriteString("title: " + v.Title + "\n")
	if v.Body != "" {
		b.WriteString("body: " + v.Body + "\n")
	}
	b.WriteString("read: " + strconv.FormatBool(v.Read) + "\n")
	b.WriteString("created_at: " + v.CreatedAt + "\n")
	return strings.TrimSuffix(b.String(), "\n")
}

// viewOf converts a stored record to its wire representation.
func viewOf(rec notify.Record) notificationView {
	view := notificationView{
		ID:        rec.ID,
		Event:     rec.Event,
		Type:      string(rec.Type),
		Surfaces:  rec.Surfaces.String(),
		Title:     rec.Title,
		Body:      rec.Body,
		Link:      rec.Link,
		Read:      !rec.ReadAt.IsZero(),
		Dismissed: !rec.DismissedAt.IsZero(),
		CreatedAt: rec.CreatedAt.UTC().Format(timeFormat),
	}
	if view.Read {
		view.ReadAt = rec.ReadAt.UTC().Format(timeFormat)
	}
	if view.Dismissed {
		view.DismissedAt = rec.DismissedAt.UTC().Format(timeFormat)
	}
	return view
}

// timeFormat is the wire timestamp format used across this endpoint group.
const timeFormat = "2006-01-02T15:04:05Z"

// List handles "GET /notifications": a paginated, optionally unread-only
// listing of the caller's own notifications, newest first.
func (n *Notifications) List(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		WriteError(w, r, errors.New(errors.CodeMethodNotAllowed, http.StatusMethodNotAllowed, "that method is not allowed here"))
		return
	}
	audience, ownerID, ok := n.caller(w, r)
	if !ok {
		return
	}

	page, limit := Paginate(r)
	unreadOnly := isTruthyQuery(r.URL.Query().Get("unread"))

	records, err := n.opts.Store.List(r.Context(), audience, ownerID, notify.ListOptions{
		UnreadOnly: unreadOnly,
		Limit:      limit,
		Offset:     (page - 1) * limit,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}

	views := make([]notificationView, 0, len(records))
	for _, rec := range records {
		views = append(views, viewOf(rec))
	}

	total := len(views) + (page-1)*limit
	if len(views) == limit {
		// The store does not report a total independent of the page, so a
		// full page is reported as "at least one more page exists" by
		// padding the total past the current offset. This keeps Pagination
		// monotonic without a second query on every list request.
		total++
	}
	WritePage(w, r, views, total)
}

// isTruthyQuery reports whether a query parameter value is one of the
// project's accepted truthy words.
func isTruthyQuery(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "enable", "enabled":
		return true
	default:
		return false
	}
}

// Count handles "GET /notifications/count": the caller's unread badge
// count, returned bare (unwrapped) since it is a single scalar value.
func (n *Notifications) Count(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		WriteError(w, r, errors.New(errors.CodeMethodNotAllowed, http.StatusMethodNotAllowed, "that method is not allowed here"))
		return
	}
	audience, ownerID, ok := n.caller(w, r)
	if !ok {
		return
	}
	count, err := n.opts.Store.UnreadCount(r.Context(), audience, ownerID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteItem(w, r, http.StatusOK, struct {
		Unread int64 `json:"unread"`
	}{Unread: count})
}

// notificationID extracts the "{id}" path segment mounted by Routes.
func notificationID(r *http.Request) string {
	return r.PathValue("id")
}

// MarkRead handles "POST /notifications/{id}/read".
func (n *Notifications) MarkRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		WriteError(w, r, errors.New(errors.CodeMethodNotAllowed, http.StatusMethodNotAllowed, "that method is not allowed here"))
		return
	}
	audience, ownerID, ok := n.caller(w, r)
	if !ok {
		return
	}
	id := notificationID(r)
	if id == "" {
		WriteError(w, r, errors.New(errors.CodeBadRequest, http.StatusBadRequest, "a notification id is required"))
		return
	}
	if err := n.opts.Store.MarkRead(r.Context(), audience, ownerID, id); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteSuccess(w, r, http.StatusOK, struct {
		ID string `json:"id"`
	}{ID: id})
}

// MarkAllRead handles "POST /notifications/read-all".
func (n *Notifications) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		WriteError(w, r, errors.New(errors.CodeMethodNotAllowed, http.StatusMethodNotAllowed, "that method is not allowed here"))
		return
	}
	audience, ownerID, ok := n.caller(w, r)
	if !ok {
		return
	}
	if err := n.opts.Store.MarkAllRead(r.Context(), audience, ownerID); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteSuccess(w, r, http.StatusOK, struct {
		Marked bool `json:"marked_all_read"`
	}{Marked: true})
}

// Delete handles "DELETE /notifications/{id}". The store has no hard
// single-record delete: only Dismiss (soft, single) and Erase (hard,
// bulk-all-for-owner) exist. Dismiss is the closest single-record removal
// semantic and is what this endpoint calls; a dismissed notification stops
// counting toward the unread badge and drops out of the default listing,
// but is not physically deleted until the retention sweep or an Erase.
func (n *Notifications) Delete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.Header().Set("Allow", "DELETE")
		WriteError(w, r, errors.New(errors.CodeMethodNotAllowed, http.StatusMethodNotAllowed, "that method is not allowed here"))
		return
	}
	audience, ownerID, ok := n.caller(w, r)
	if !ok {
		return
	}
	id := notificationID(r)
	if id == "" {
		WriteError(w, r, errors.New(errors.CodeBadRequest, http.StatusBadRequest, "a notification id is required"))
		return
	}
	if err := n.opts.Store.Dismiss(r.Context(), audience, ownerID, id); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteSuccess(w, r, http.StatusOK, struct {
		ID string `json:"id"`
	}{ID: id})
}

// Routes describes every notifications route, canonical and unversioned
// alias, for src/server.Server.MountRoute/MountAlias. Each alias mounts the
// exact same handler instance as its canonical route, never a redirect.
func (n *Notifications) Routes() []Route {
	list := http.HandlerFunc(n.List)
	count := http.HandlerFunc(n.Count)
	markRead := http.HandlerFunc(n.MarkRead)
	markAllRead := http.HandlerFunc(n.MarkAllRead)
	del := http.HandlerFunc(n.Delete)

	listPattern := APIPath("notifications")
	countPattern := APIPath("notifications", "count")
	markReadPattern := APIPath("notifications", "{id}", "read")
	markAllReadPattern := APIPath("notifications", "read-all")
	deletePattern := APIPath("notifications", "{id}")

	return []Route{
		{
			Method: http.MethodGet, Pattern: listPattern, Name: "listNotifications",
			Summary: "List the caller's notifications", Auth: true, Tags: []string{"notifications"},
			Handler: list,
		},
		{
			Method: http.MethodGet, Pattern: UnversionedPath("notifications"), Name: "listNotificationsAlias",
			Alias: true, Canonical: listPattern, Auth: true, Tags: []string{"notifications"}, Internal: true,
			Handler: list,
		},
		{
			Method: http.MethodGet, Pattern: countPattern, Name: "countNotifications",
			Summary: "The caller's unread notification count", Auth: true, Bare: true, Tags: []string{"notifications"},
			Handler: count,
		},
		{
			Method: http.MethodGet, Pattern: UnversionedPath("notifications", "count"), Name: "countNotificationsAlias",
			Alias: true, Canonical: countPattern, Auth: true, Bare: true, Tags: []string{"notifications"}, Internal: true,
			Handler: count,
		},
		{
			Method: http.MethodPost, Pattern: markReadPattern, Name: "markNotificationRead",
			Summary: "Mark one notification read", Auth: true, Tags: []string{"notifications"},
			Handler: markRead,
		},
		{
			Method: http.MethodPost, Pattern: UnversionedPath("notifications", "{id}", "read"), Name: "markNotificationReadAlias",
			Alias: true, Canonical: markReadPattern, Auth: true, Tags: []string{"notifications"}, Internal: true,
			Handler: markRead,
		},
		{
			Method: http.MethodPost, Pattern: markAllReadPattern, Name: "markAllNotificationsRead",
			Summary: "Mark every notification read", Auth: true, Tags: []string{"notifications"},
			Handler: markAllRead,
		},
		{
			Method: http.MethodPost, Pattern: UnversionedPath("notifications", "read-all"), Name: "markAllNotificationsReadAlias",
			Alias: true, Canonical: markAllReadPattern, Auth: true, Tags: []string{"notifications"}, Internal: true,
			Handler: markAllRead,
		},
		{
			Method: http.MethodDelete, Pattern: deletePattern, Name: "deleteNotification",
			Summary: "Dismiss one notification", Auth: true, Tags: []string{"notifications"},
			Handler: del,
		},
		{
			Method: http.MethodDelete, Pattern: UnversionedPath("notifications", "{id}"), Name: "deleteNotificationAlias",
			Alias: true, Canonical: deletePattern, Auth: true, Tags: []string{"notifications"}, Internal: true,
			Handler: del,
		},
	}
}
