package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/webappsgo/cashp/src/database"
)

// Store is the persistence layer for every auth, organization and custom-domain
// table. Every statement is parameterized; no query is ever assembled by
// concatenating a caller-supplied value.
type Store struct {
	db *database.DB
}

// NewStore wraps an open database handle.
func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

// DB exposes the underlying handle for callers that need a transaction.
func (s *Store) DB() *database.DB { return s.db }

// q rewrites the portable `?` placeholders into the driver's own syntax.
func (s *Store) q(query string) string { return s.db.Rebind(query) }

// isNoRows reports whether err means "nothing matched", across both the stdlib
// sentinel and the wrapper used by the database package.
func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, database.ErrNotFound)
}

// lastID extracts the auto-generated primary key from an insert result. Drivers that
// do not support LastInsertId return zero, which callers treat as "look it up".
func lastID(res sql.Result) int64 {
	if res == nil {
		return 0
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0
	}
	return id
}

// boolInt converts a Go bool into the 0/1 integer stored in every driver.
func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// scopesJSON serializes a scope list into the compact JSON array kept in the token rows.
func scopesJSON(scopes []string) string {
	clean := make([]string, 0, len(scopes))
	for _, sc := range scopes {
		sc = strings.TrimSpace(sc)
		if sc == "" {
			continue
		}
		clean = append(clean, sc)
	}
	if len(clean) == 0 {
		return "[]"
	}
	return `["` + strings.Join(clean, `","`) + `"]`
}

// scopeList parses the JSON array stored in a token row back into a slice.
func scopeList(raw string) []string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), `"`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Ping verifies the handle is usable, used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect, s.q(`SELECT 1`)).Scan(&one)
	if err != nil && !isNoRows(err) {
		return err
	}
	return nil
}
