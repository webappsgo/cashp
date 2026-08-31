package notify

import (
	"context"
	"database/sql"
	"net/http"
	"sort"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/errors"
)

// ErrPreferenceRequired rejects an attempt to switch off a security event.
// AI.md PART 18 -> "Notification Preferences": "Security notifications
// cannot be disabled - these are critical for account security."
var ErrPreferenceRequired = errors.New(errors.CodeValidation, http.StatusBadRequest, "security notifications cannot be disabled")

// ErrUnknownEvent rejects a preference for an event that is not in the
// catalog, so a stale form cannot write rows nothing will ever read.
var ErrUnknownEvent = errors.New(errors.CodeValidation, http.StatusBadRequest, "unknown notification event")

// Preference is one account's routing choice for one event, resolved
// against the catalog defaults.
type Preference struct {
	// Event is the catalog event name.
	Event string
	// Category is the event's preference group.
	Category string
	// Title is the human-readable event name for the form row.
	Title string
	// WebUI reports whether the notification center stores this event.
	WebUI bool
	// Email reports whether this event is emailed.
	Email bool
	// Required marks a security event whose toggles are fixed on.
	Required bool
	// Emailable reports whether the event has an email template at all. A
	// non-emailable event renders its email toggle disabled.
	Emailable bool
	// Stored reports whether the account has an explicit saved row, as
	// opposed to inheriting the shipped default.
	Stored bool
}

// Toggle is one incoming preference change from the settings form.
type Toggle struct {
	// Event is the catalog event name.
	Event string
	// WebUI is the requested notification-center setting.
	WebUI bool
	// Email is the requested email setting.
	Email bool
}

// defaultPreference resolves the shipped default for one event.
func defaultPreference(event Event) Preference {
	pref := Preference{
		Event:     event.Name,
		Category:  event.Category,
		Title:     event.Title,
		WebUI:     event.DefaultWebUI,
		Email:     event.DefaultEmail,
		Required:  event.Required,
		Emailable: event.Template != "",
	}
	// A required event ignores its stored row entirely: both surfaces stay
	// on for as long as the event has somewhere to go.
	if event.Required {
		pref.WebUI = true
		pref.Email = pref.Emailable
	}
	return pref
}

// Preferences returns every event that applies to one account, with the
// account's saved choices merged over the catalog defaults. Events the
// audience never receives are omitted.
func (s *Store) Preferences(ctx context.Context, audience Audience, ownerID string) ([]Preference, error) {
	stored, err := s.storedPreferences(ctx, audience, ownerID)
	if err != nil {
		return nil, err
	}

	var out []Preference
	for _, event := range Events() {
		if !event.Audience.Includes(audience) {
			continue
		}
		pref := defaultPreference(event)
		if saved, ok := stored[event.Name]; ok && !event.Required {
			pref.WebUI = saved.WebUI
			pref.Email = saved.Email && pref.Emailable
			pref.Stored = true
		}
		out = append(out, pref)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Event < out[j].Event
	})
	return out, nil
}

// storedPreferences reads one account's saved rows.
func (s *Store) storedPreferences(ctx context.Context, audience Audience, ownerID string) (map[string]Toggle, error) {
	query := "SELECT event, webui, email FROM " + TablePreferences + " WHERE audience = ? AND owner_id = ?"
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect, query, string(audience), ownerID)
	if err != nil {
		return nil, database.Classify(err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]Toggle{}
	for rows.Next() {
		var (
			event string
			webui int64
			email int64
		)
		if err := rows.Scan(&event, &webui, &email); err != nil {
			return nil, database.Classify(err)
		}
		out[event] = Toggle{Event: event, WebUI: webui != 0, Email: email != 0}
	}
	if err := rows.Err(); err != nil {
		return nil, database.Classify(err)
	}
	return out, nil
}

// SavePreferences writes an account's preference form. Every toggle is
// validated against the catalog first, so a partially valid submission
// changes nothing.
func (s *Store) SavePreferences(ctx context.Context, audience Audience, ownerID string, toggles []Toggle) error {
	if ownerID == "" {
		return errors.New(errors.CodeValidation, http.StatusBadRequest, "preferences need an account identifier")
	}

	checked := make([]Toggle, 0, len(toggles))
	for _, toggle := range toggles {
		event, ok := Lookup(toggle.Event)
		if !ok || !event.Audience.Includes(audience) {
			return ErrUnknownEvent.WithDetails(map[string]any{"event": toggle.Event})
		}
		if event.Required && (!toggle.WebUI || (event.Template != "" && !toggle.Email)) {
			return ErrPreferenceRequired.WithDetails(map[string]any{"event": toggle.Event})
		}
		if event.Template == "" {
			toggle.Email = false
		}
		checked = append(checked, toggle)
	}

	now := s.now().Unix()
	err := s.db.Tx(ctx, database.TimeoutWrite, func(tx *sql.Tx) error {
		update := s.db.Rebind("UPDATE " + TablePreferences + " SET webui = ?, email = ?, updated_at = ? WHERE audience = ? AND owner_id = ? AND event = ?")
		insert := s.db.Rebind("INSERT INTO " + TablePreferences + " (audience, owner_id, event, webui, email, updated_at) VALUES (?, ?, ?, ?, ?, ?)")

		for _, toggle := range checked {
			res, err := tx.ExecContext(ctx, update, boolInt(toggle.WebUI), boolInt(toggle.Email), now, string(audience), ownerID, toggle.Event)
			if err != nil {
				return err
			}
			affected, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if affected > 0 {
				continue
			}
			// No row existed, so the account is still on the shipped
			// default. An insert race is harmless: the duplicate loses and
			// the winning row already carries this submission's values.
			if _, err := tx.ExecContext(ctx, insert, string(audience), ownerID, toggle.Event, boolInt(toggle.WebUI), boolInt(toggle.Email), now); err != nil {
				if isDuplicateRow(err) {
					continue
				}
				return err
			}
		}
		return nil
	})
	if err != nil {
		return database.Classify(err)
	}

	return s.Audit(ctx, AuditEntry{
		Actor:  ownerID,
		Action: ActionPreference,
		Result: "saved",
		Detail: "notification preferences updated",
	})
}

// Routing resolves where one event goes for one account. A missing account
// identifier, an unknown event or an unreadable preference row all fall
// back to the catalog default rather than dropping the notification.
func (s *Store) Routing(ctx context.Context, audience Audience, ownerID, eventName string) (webui, email bool, err error) {
	event, ok := Lookup(eventName)
	if !ok {
		return true, false, ErrUnknownEvent.WithDetails(map[string]any{"event": eventName})
	}

	pref := defaultPreference(event)
	if ownerID == "" || event.Required {
		return pref.WebUI, pref.Email, nil
	}

	query := "SELECT webui, email FROM " + TablePreferences + " WHERE audience = ? AND owner_id = ? AND event = ?"
	var storedWebUI, storedEmail int64
	scanErr := s.db.QueryRowContext(ctx, database.TimeoutSelect, query, string(audience), ownerID, eventName).Scan(&storedWebUI, &storedEmail)
	if database.IsNotFound(scanErr) {
		return pref.WebUI, pref.Email, nil
	}
	if scanErr != nil {
		return pref.WebUI, pref.Email, database.Classify(scanErr)
	}
	return storedWebUI != 0, storedEmail != 0 && pref.Emailable, nil
}

// ResetPreferences drops an account's saved rows so it inherits the shipped
// defaults again.
func (s *Store) ResetPreferences(ctx context.Context, audience Audience, ownerID string) error {
	query := "DELETE FROM " + TablePreferences + " WHERE audience = ? AND owner_id = ?"
	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite, query, string(audience), ownerID); err != nil {
		return database.Classify(err)
	}
	return s.Audit(ctx, AuditEntry{
		Actor:  ownerID,
		Action: ActionPreference,
		Result: "reset",
		Detail: "notification preferences reset to defaults",
	})
}

// boolInt renders a boolean for the integer columns the schema uses, which
// is the only representation portable across all five supported drivers.
func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
