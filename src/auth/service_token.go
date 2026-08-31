package auth

import (
	"context"
	"log/slog"
	"strings"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// MaxTokensPerOwner caps how many live API tokens one user or org may hold.
const MaxTokensPerOwner = 50

// KnownScopes is the closed set an API token may carry. Anything outside it is
// rejected at creation, so a typo can never silently widen a token's reach.
var KnownScopes = []string{
	"*",
	"profile", "profile:read", "profile:write",
	"orgs", "orgs:read", "orgs:write",
	"members", "members:read", "members:write",
	"domains", "domains:read", "domains:write",
	"tokens", "tokens:read", "tokens:write",
	"sites", "sites:read", "sites:write",
	"databases", "databases:read", "databases:write",
	"containers", "containers:read", "containers:write",
	"metrics", "metrics:read",
	"backups", "backups:read", "backups:write",
}

// TokenInput describes a token to mint.
type TokenInput struct {
	Name      string
	Scopes    []string
	ExpiresAt int64
}

// normalizeScopes validates and de-duplicates a requested scope set.
func normalizeScopes(in []string) ([]string, *apperr.Error) {
	if len(in) == 0 {
		return nil, ErrValidation("scopes", "Select at least one scope for this token")
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		scope := strings.ToLower(strings.TrimSpace(raw))
		if scope == "" || seen[scope] {
			continue
		}
		known := false
		for _, k := range KnownScopes {
			if k == scope {
				known = true
				break
			}
		}
		if !known {
			return nil, ErrValidation("scopes", "That scope is not recognised")
		}
		seen[scope] = true
		out = append(out, scope)
	}
	if len(out) == 0 {
		return nil, ErrValidation("scopes", "Select at least one scope for this token")
	}
	return out, nil
}

// validateTokenInput applies the rules shared by user and org tokens.
func validateTokenInput(in TokenInput) (name string, scopes []string, aerr *apperr.Error) {
	name = strings.TrimSpace(in.Name)
	if name == "" || len(name) > 100 {
		return "", nil, ErrValidation("name", "Give the token a name of 1-100 characters")
	}
	scopes, aerr = normalizeScopes(in.Scopes)
	if aerr != nil {
		return "", nil, aerr
	}
	if in.ExpiresAt != 0 && in.ExpiresAt <= time.Now().Unix() {
		return "", nil, ErrValidation("expires_at", "Choose an expiry in the future")
	}
	return name, scopes, nil
}

// CreateUserToken mints a user-owned API token. The plaintext is returned exactly once,
// in this response; only its SHA-256 hash is persisted, so it can never be recovered or
// re-displayed afterwards.
func (s *Service) CreateUserToken(ctx context.Context, userID int64, in TokenInput) (PublicToken, *apperr.Error) {
	name, scopes, aerr := validateTokenInput(in)
	if aerr != nil {
		return PublicToken{}, aerr
	}
	existing, err := s.store.ListUserTokens(ctx, userID)
	if err != nil {
		return PublicToken{}, ErrInternal(err)
	}
	if len(existing) >= MaxTokensPerOwner {
		return PublicToken{}, ErrQuota("You have reached the maximum number of API tokens")
	}

	plaintext, hash, err := security.GenerateToken(security.PrefixUser)
	if err != nil {
		return PublicToken{}, ErrInternal(err)
	}
	tok := &Token{
		OwnerType:   OwnerUser,
		OwnerID:     userID,
		Name:        name,
		TokenHash:   hash,
		TokenPrefix: security.TokenDisplayPrefix(plaintext),
		Scopes:      scopesJSON(scopes),
		ExpiresAt:   in.ExpiresAt,
	}
	if _, err := s.store.CreateUserToken(ctx, tok); err != nil {
		return PublicToken{}, ErrInternal(err)
	}
	s.audit("token.created",
		slog.String("owner_type", OwnerUser),
		slog.Int64("owner_id", userID),
		slog.Int64("token_id", tok.ID),
		slog.String("name", name))

	out := publicToken(tok)
	out.Token = plaintext
	return out, nil
}

// CreateOrgToken mints an organization-owned API token.
func (s *Service) CreateOrgToken(ctx context.Context, orgID, actorID int64, in TokenInput) (PublicToken, *apperr.Error) {
	name, scopes, aerr := validateTokenInput(in)
	if aerr != nil {
		return PublicToken{}, aerr
	}
	existing, err := s.store.ListOrgTokens(ctx, orgID)
	if err != nil {
		return PublicToken{}, ErrInternal(err)
	}
	if len(existing) >= MaxTokensPerOwner {
		return PublicToken{}, ErrQuota("This organization has reached the maximum number of API tokens")
	}

	plaintext, hash, err := security.GenerateToken(security.PrefixOrg)
	if err != nil {
		return PublicToken{}, ErrInternal(err)
	}
	tok := &Token{
		OwnerType:   OwnerOrg,
		OwnerID:     orgID,
		Name:        name,
		TokenHash:   hash,
		TokenPrefix: security.TokenDisplayPrefix(plaintext),
		Scopes:      scopesJSON(scopes),
		ExpiresAt:   in.ExpiresAt,
	}
	if _, err := s.store.CreateOrgToken(ctx, tok, actorID); err != nil {
		return PublicToken{}, ErrInternal(err)
	}
	if err := s.store.RecordOrgAudit(ctx, orgID, "token.created", OwnerUser, actorID, name); err != nil {
		s.log.Warn("record org audit", slog.String("error", err.Error()))
	}
	s.audit("token.created",
		slog.String("owner_type", OwnerOrg),
		slog.Int64("owner_id", orgID),
		slog.Int64("token_id", tok.ID),
		slog.Int64("actor_id", actorID),
		slog.String("name", name))

	out := publicToken(tok)
	out.Token = plaintext
	return out, nil
}

// ListUserTokens returns a user's tokens. The hash is never included and the plaintext
// is long gone, so this listing is safe to render.
func (s *Service) ListUserTokens(ctx context.Context, userID int64) ([]PublicToken, *apperr.Error) {
	rows, err := s.store.ListUserTokens(ctx, userID)
	if err != nil {
		return nil, ErrInternal(err)
	}
	return publicTokens(rows), nil
}

// ListOrgTokens returns an organization's tokens.
func (s *Service) ListOrgTokens(ctx context.Context, orgID int64) ([]PublicToken, *apperr.Error) {
	rows, err := s.store.ListOrgTokens(ctx, orgID)
	if err != nil {
		return nil, ErrInternal(err)
	}
	return publicTokens(rows), nil
}

// RevokeUserToken revokes a token, scoped to its owner in the UPDATE itself so one user
// cannot revoke another user's token by supplying its ID.
func (s *Service) RevokeUserToken(ctx context.Context, userID, tokenID int64) *apperr.Error {
	if err := s.store.RevokeUserToken(ctx, userID, tokenID); err != nil {
		return ErrInternal(err)
	}
	s.audit("token.revoked",
		slog.String("owner_type", OwnerUser),
		slog.Int64("owner_id", userID),
		slog.Int64("token_id", tokenID))
	return nil
}

// RevokeOrgToken revokes an organization token, scoped to that organization.
func (s *Service) RevokeOrgToken(ctx context.Context, orgID, actorID, tokenID int64) *apperr.Error {
	if err := s.store.RevokeOrgToken(ctx, orgID, tokenID); err != nil {
		return ErrInternal(err)
	}
	if err := s.store.RecordOrgAudit(ctx, orgID, "token.revoked", OwnerUser, actorID, ""); err != nil {
		s.log.Warn("record org audit", slog.String("error", err.Error()))
	}
	s.audit("token.revoked",
		slog.String("owner_type", OwnerOrg),
		slog.Int64("owner_id", orgID),
		slog.Int64("actor_id", actorID),
		slog.Int64("token_id", tokenID))
	return nil
}

// ListSessions returns a user's active sessions for the security page.
func (s *Service) ListSessions(ctx context.Context, userID int64) ([]*Session, *apperr.Error) {
	rows, err := s.store.ListSessions(ctx, userID)
	if err != nil {
		return nil, ErrInternal(err)
	}
	return rows, nil
}

// RevokeSession ends one of the caller's own sessions.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID int64) *apperr.Error {
	if err := s.store.DeleteSession(ctx, userID, sessionID); err != nil {
		return ErrInternal(err)
	}
	s.audit("session.revoked", slog.Int64("user_id", userID), slog.Int64("session_id", sessionID))
	return nil
}

// RevokeAllSessions ends every session the user holds.
func (s *Service) RevokeAllSessions(ctx context.Context, userID int64) *apperr.Error {
	if err := s.store.DeleteUserSessions(ctx, userID); err != nil {
		return ErrInternal(err)
	}
	s.audit("session.revoked_all", slog.Int64("user_id", userID))
	return nil
}

// publicToken converts one stored token into its response shape.
func publicToken(t *Token) PublicToken {
	return PublicToken{
		ID:          t.ID,
		Name:        t.Name,
		TokenPrefix: t.TokenPrefix,
		Scopes:      scopeList(t.Scopes),
		ExpiresAt:   t.ExpiresAt,
		LastUsedAt:  t.LastUsedAt,
		CreatedAt:   t.CreatedAt,
		Revoked:     t.Revoked,
	}
}

// publicTokens converts a listing into response shapes.
func publicTokens(rows []*Token) []PublicToken {
	out := make([]PublicToken, 0, len(rows))
	for _, t := range rows {
		out = append(out, publicToken(t))
	}
	return out
}
