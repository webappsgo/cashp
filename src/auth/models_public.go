package auth

// PublicSession is the response shape for an active session. The stored token hash is
// never included, so the security page can be rendered without handling a secret.
type PublicSession struct {
	ID        int64  `json:"id"`
	IPAddress string `json:"ip_address"`
	UserAgent string `json:"user_agent"`
	Location  string `json:"location,omitempty"`
	Current   bool   `json:"current"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}

// publicSessions converts a session listing, flagging the caller's own session so the
// interface can label it rather than inviting the user to revoke themselves by accident.
func publicSessions(rows []*Session, currentID int64) []PublicSession {
	out := make([]PublicSession, 0, len(rows))
	for _, s := range rows {
		out = append(out, PublicSession{
			ID:        s.ID,
			IPAddress: s.IPAddress,
			UserAgent: s.UserAgent,
			Location:  s.Location,
			Current:   s.ID == currentID,
			CreatedAt: s.CreatedAt,
			ExpiresAt: s.ExpiresAt,
		})
	}
	return out
}

// PublicMember is the response shape for an organization member.
type PublicMember struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	JoinedAt int64  `json:"joined_at"`
}

// publicMembers converts a membership roster.
func publicMembers(rows []*OrgMember) []PublicMember {
	out := make([]PublicMember, 0, len(rows))
	for _, m := range rows {
		out = append(out, PublicMember{
			UserID:   m.UserID,
			Username: m.Username,
			Role:     m.Role,
			JoinedAt: m.JoinedAt,
		})
	}
	return out
}

// PublicInvite is the response shape for an outstanding invitation. The invite code is
// stored only as a hash and is shown in full exactly once, at creation, so a listing can
// never reveal it.
type PublicInvite struct {
	ID        int64  `json:"id"`
	Email     string `json:"email,omitempty"`
	Role      string `json:"role"`
	MaxUses   int    `json:"max_uses"`
	UseCount  int    `json:"use_count"`
	ExpiresAt int64  `json:"expires_at"`
	CreatedAt int64  `json:"created_at"`
	Revoked   bool   `json:"revoked"`
	// Code carries the plaintext invitation code in the creation response only.
	Code string `json:"code,omitempty"`
}

// publicInvites converts an invitation listing.
func publicInvites(rows []*Invite) []PublicInvite {
	out := make([]PublicInvite, 0, len(rows))
	for _, i := range rows {
		out = append(out, PublicInvite{
			ID:        i.ID,
			Email:     i.Email,
			Role:      i.Role,
			MaxUses:   i.MaxUses,
			UseCount:  i.UseCount,
			ExpiresAt: i.ExpiresAt,
			CreatedAt: i.CreatedAt,
			Revoked:   i.Revoked,
		})
	}
	return out
}

// PublicDomain is the response shape for a custom domain. Certificate material and
// provider credentials stay on the server and are never serialized.
type PublicDomain struct {
	ID                 int64  `json:"id"`
	Domain             string `json:"domain"`
	OwnerType          string `json:"owner_type"`
	IsApex             bool   `json:"is_apex"`
	IsWildcard         bool   `json:"is_wildcard"`
	VerificationStatus string `json:"verification_status"`
	Status             string `json:"status"`
	SuspendedReason    string `json:"suspended_reason,omitempty"`
	SSLEnabled         bool   `json:"ssl_enabled"`
	SSLStatus          string `json:"ssl_status"`
	SSLChallenge       string `json:"ssl_challenge,omitempty"`
	SSLExpiresAt       int64  `json:"ssl_expires_at,omitempty"`
	VerifiedAt         int64  `json:"verified_at,omitempty"`
	CreatedAt          int64  `json:"created_at"`
	// RecordName and RecordValue are populated only while ownership is unproven, so a
	// verified domain stops echoing its token.
	RecordName  string `json:"record_name,omitempty"`
	RecordValue string `json:"record_value,omitempty"`
}

// publicDomain converts one domain, attaching the DNS instructions while pending.
func (s *Service) publicDomain(d *CustomDomain) PublicDomain {
	out := PublicDomain{
		ID:                 d.ID,
		Domain:             d.Domain,
		OwnerType:          d.OwnerType,
		IsApex:             d.IsApex,
		IsWildcard:         d.IsWildcard,
		VerificationStatus: d.VerificationStatus,
		Status:             d.Status,
		SuspendedReason:    d.SuspendedReason,
		SSLEnabled:         d.SSLEnabled,
		SSLStatus:          d.SSLStatus,
		SSLChallenge:       d.SSLChallenge,
		SSLExpiresAt:       d.SSLExpiresAt,
		VerifiedAt:         d.VerifiedAt,
		CreatedAt:          d.CreatedAt,
	}
	if d.VerificationStatus != VerificationVerified {
		out.RecordName, out.RecordValue = s.VerificationInstructions(d)
	}
	return out
}

// publicDomains converts a domain listing.
func (s *Service) publicDomains(rows []*CustomDomain) []PublicDomain {
	out := make([]PublicDomain, 0, len(rows))
	for _, d := range rows {
		out = append(out, s.publicDomain(d))
	}
	return out
}
