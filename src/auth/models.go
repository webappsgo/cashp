package auth

import "time"

// Role values for the users table. They map onto the IDEA.md role table:
// global_admin is a Server Admin (admins table, PART 17), account_admin maps to an
// organization Owner/Admin, and end_user maps to an organization Member.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// Organization member roles from AI.md PART 35 "Org Member Roles".
const (
	OrgRoleOwner  = "owner"
	OrgRoleAdmin  = "admin"
	OrgRoleMember = "member"
)

// Registration modes from AI.md PART 34 "Registration Modes".
const (
	RegistrationOpen      = "open"
	RegistrationInvite    = "invite"
	RegistrationAdminOnly = "admin_only"
	RegistrationDisabled  = "disabled"
)

// Organization creation modes from AI.md PART 35 "Organization Creation Modes".
const (
	OrgCreationOpen      = "open"
	OrgCreationInvite    = "invite"
	OrgCreationAdminOnly = "admin_only"
	OrgCreationDisabled  = "disabled"
)

// Owner types shared by tokens and custom domains.
const (
	OwnerUser  = "user"
	OwnerOrg   = "org"
	OwnerAdmin = "admin"
)

// Custom domain verification states from AI.md PART 36 "Database Schema".
const (
	VerificationPending  = "pending"
	VerificationVerified = "verified"
	VerificationFailed   = "failed"
)

// Custom domain lifecycle states from AI.md PART 36 "Database Schema".
const (
	DomainStatusPending   = "pending"
	DomainStatusActive    = "active"
	DomainStatusSuspended = "suspended"
	DomainStatusError     = "error"
)

// Custom domain SSL states from AI.md PART 36 "Database Schema".
const (
	SSLStatusNone    = "none"
	SSLStatusPending = "pending"
	SSLStatusActive  = "active"
	SSLStatusExpired = "expired"
	SSLStatusError   = "error"
)

// ACME challenge identifiers from AI.md PART 36 "SSL Challenge Types".
const (
	ChallengeHTTP01    = "http-01"
	ChallengeTLSALPN01 = "tls-alpn-01"
	ChallengeDNS01     = "dns-01"
)

// Visibility values shared by user and org profiles.
const (
	VisibilityPublic  = "public"
	VisibilityPrivate = "private"
)

// SessionTTL is how long a freshly issued user session stays valid.
const SessionTTL = 30 * 24 * time.Hour

// AdminSessionTTL is how long a Server Admin panel session stays valid. It is much
// shorter than a user session because the panel controls the whole server.
const AdminSessionTTL = 12 * time.Hour

// SetupTokenTTL bounds how long the primary-admin bootstrap token may be redeemed.
const SetupTokenTTL = time.Hour

// PasswordResetTTL bounds how long a password reset token may be redeemed.
const PasswordResetTTL = time.Hour

// EmailVerificationTTL bounds how long an email verification token may be redeemed.
const EmailVerificationTTL = 24 * time.Hour

// InviteTTL is the default lifetime of a user or org invite.
const InviteTTL = 7 * 24 * time.Hour

// LockoutThreshold is the number of consecutive failed logins that locks an account.
const LockoutThreshold = 10

// LockoutDuration is how long an account stays locked after crossing the threshold.
const LockoutDuration = 15 * time.Minute

// User is a regular end-user account stored in the users database.
// PasswordHash and TOTPSecret are never serialized to any response.
type User struct {
	ID            int64
	Username      string
	Email         string
	PasswordHash  string
	DisplayName   string
	AvatarURL     string
	Bio           string
	Location      string
	Website       string
	Visibility    string
	Role          string
	Source        string
	ExternalID    string
	Groups        string
	LastSync      int64
	EmailVerified bool
	Approved      bool
	Disabled      bool
	TOTPSecret    string
	TOTPEnabled   bool
	Timezone      string
	Language      string
	CreatedAt     int64
	UpdatedAt     int64
	LastLoginAt   int64
	FailedLogins  int
	LockedUntil   int64
}

// Locked reports whether the account is currently within a lockout window.
func (u *User) Locked() bool {
	return u.LockedUntil > time.Now().Unix()
}

// PublicUser is the response shape for a user profile. It exists so that no code path
// can accidentally marshal a User and leak the password hash or TOTP secret.
type PublicUser struct {
	ID            int64  `json:"id"`
	Username      string `json:"username"`
	Email         string `json:"email,omitempty"`
	DisplayName   string `json:"display_name"`
	AvatarURL     string `json:"avatar_url,omitempty"`
	Bio           string `json:"bio,omitempty"`
	Location      string `json:"location,omitempty"`
	Website       string `json:"website,omitempty"`
	Visibility    string `json:"visibility"`
	Role          string `json:"role,omitempty"`
	EmailVerified bool   `json:"email_verified,omitempty"`
	TOTPEnabled   bool   `json:"totp_enabled,omitempty"`
	Timezone      string `json:"timezone,omitempty"`
	Language      string `json:"language,omitempty"`
	CreatedAt     int64  `json:"created_at"`
}

// Public returns the self-view of a user, including private fields the owner may see.
func (u *User) Public() PublicUser {
	return PublicUser{
		ID:            u.ID,
		Username:      u.Username,
		Email:         u.Email,
		DisplayName:   u.DisplayName,
		AvatarURL:     u.AvatarURL,
		Bio:           u.Bio,
		Location:      u.Location,
		Website:       u.Website,
		Visibility:    u.Visibility,
		Role:          u.Role,
		EmailVerified: u.EmailVerified,
		TOTPEnabled:   u.TOTPEnabled,
		Timezone:      u.Timezone,
		Language:      u.Language,
		CreatedAt:     u.CreatedAt,
	}
}

// Profile returns the third-party view of a user, omitting every private field.
func (u *User) Profile() PublicUser {
	return PublicUser{
		ID:          u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		AvatarURL:   u.AvatarURL,
		Bio:         u.Bio,
		Location:    u.Location,
		Website:     u.Website,
		Visibility:  u.Visibility,
		CreatedAt:   u.CreatedAt,
	}
}

// Session is an authenticated browser session. Only the SHA-256 hash of the session
// token is persisted; the plaintext lives solely in the client cookie.
type Session struct {
	ID        int64
	UserID    int64
	TokenHash string
	IPAddress string
	UserAgent string
	Location  string
	ExpiresAt int64
	CreatedAt int64
}

// Expired reports whether the session is past its expiry.
func (s *Session) Expired() bool {
	return time.Now().Unix() > s.ExpiresAt
}

// Token is an API token owned by a user, org, or admin.
type Token struct {
	ID          int64
	OwnerType   string
	OwnerID     int64
	Name        string
	TokenHash   string
	TokenPrefix string
	Scopes      string
	ExpiresAt   int64
	LastUsedAt  int64
	CreatedAt   int64
	Revoked     bool
}

// Expired reports whether the token has passed its optional expiry.
func (t *Token) Expired() bool {
	return t.ExpiresAt > 0 && time.Now().Unix() > t.ExpiresAt
}

// Usable reports whether the token may authenticate a request.
func (t *Token) Usable() bool {
	return !t.Revoked && !t.Expired()
}

// PublicToken is the response shape for a token. The hash is never included, and the
// plaintext value is present only in the single creation response.
type PublicToken struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	TokenPrefix string   `json:"token_prefix"`
	Scopes      []string `json:"scopes"`
	ExpiresAt   int64    `json:"expires_at,omitempty"`
	LastUsedAt  int64    `json:"last_used_at,omitempty"`
	CreatedAt   int64    `json:"created_at"`
	Revoked     bool     `json:"revoked"`
	Token       string   `json:"token,omitempty"`
}

// Invite is a single-use (by default) account or organization invitation.
type Invite struct {
	ID        int64
	Kind      string
	Code      string
	CodeHash  string
	Email     string
	OrgID     int64
	Role      string
	MaxUses   int
	UseCount  int
	ExpiresAt int64
	CreatedBy int64
	CreatedAt int64
	Revoked   bool
}

// Usable reports whether the invite may still be redeemed.
func (i *Invite) Usable() bool {
	if i.Revoked {
		return false
	}
	if i.ExpiresAt > 0 && time.Now().Unix() > i.ExpiresAt {
		return false
	}
	return i.MaxUses == 0 || i.UseCount < i.MaxUses
}

// Org is an organization, cashp's tenant/hosting-account entity.
type Org struct {
	ID          int64
	Slug        string
	Name        string
	Description string
	AvatarType  string
	AvatarURL   string
	Website     string
	Location    string
	Visibility  string
	OwnerID     int64
	Suspended   bool
	CreatedAt   int64
	UpdatedAt   int64
}

// PublicOrg is the response shape for an organization.
type PublicOrg struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	Website     string `json:"website,omitempty"`
	Location    string `json:"location,omitempty"`
	Visibility  string `json:"visibility"`
	MemberCount int    `json:"member_count"`
	CreatedAt   int64  `json:"created_at"`
	YourRole    string `json:"your_role,omitempty"`
}

// Public converts an Org into its response shape.
func (o *Org) Public(memberCount int, yourRole string) PublicOrg {
	return PublicOrg{
		ID:          o.ID,
		Slug:        o.Slug,
		Name:        o.Name,
		Description: o.Description,
		AvatarURL:   o.AvatarURL,
		Website:     o.Website,
		Location:    o.Location,
		Visibility:  o.Visibility,
		MemberCount: memberCount,
		CreatedAt:   o.CreatedAt,
		YourRole:    yourRole,
	}
}

// OrgMember links a user to an organization with a role.
type OrgMember struct {
	OrgID    int64
	UserID   int64
	Username string
	Role     string
	JoinedAt int64
}

// CanManageMembers reports whether the role may add, remove, or re-role other members.
func CanManageMembers(role string) bool {
	return role == OrgRoleOwner || role == OrgRoleAdmin
}

// CanManageOrg reports whether the role may change org settings and tokens.
func CanManageOrg(role string) bool {
	return role == OrgRoleOwner || role == OrgRoleAdmin
}

// CanDeleteOrg reports whether the role may delete the org or transfer ownership.
func CanDeleteOrg(role string) bool {
	return role == OrgRoleOwner
}

// CustomDomain is a user- or org-owned domain routed to the platform once verified.
// SSLCredentials, SSLCertPEM, and SSLKeyPEM are stored encrypted at rest.
type CustomDomain struct {
	ID                 int64
	OwnerType          string
	OwnerID            int64
	Domain             string
	IsApex             bool
	IsWildcard         bool
	VerificationStatus string
	VerificationToken  string
	VerifiedAt         int64
	LastCheckAt        int64
	CheckCount         int
	SSLEnabled         bool
	SSLStatus          string
	SSLChallenge       string
	SSLProvider        string
	SSLCredentials     string
	SSLCertPEM         string
	SSLKeyPEM          string
	SSLIssuedAt        int64
	SSLExpiresAt       int64
	SSLLastError       string
	Status             string
	SuspendedReason    string
	CreatedAt          int64
	UpdatedAt          int64
}

// PublicDomain (the response shape for a custom domain) and the conversion
// from CustomDomain live in models_public.go — that version reports DNS
// instructions as RecordName/RecordValue (matching PART 36's `_verify.
// {domain}` TXT flow) instead of echoing the raw verification token.
