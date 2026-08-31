package auth

import "time"

// Config carries every knob this package honours. src/server builds it from the
// loaded application config and passes it to New; the package never reaches for a
// global accessor, so tests can construct a Config directly.
type Config struct {
	// SiteName is used in TOTP provisioning URIs and outbound email subjects.
	SiteName string
	// BaseURL is the canonical external origin, e.g. https://panel.example.com.
	BaseURL string
	// AdminPath is the admin panel mount point without slashes, e.g. "server".
	AdminPath string
	// APIVersion is the API mount version segment, e.g. "v1".
	APIVersion string
	// Secure marks session cookies Secure. Disable only for plain-HTTP local development.
	Secure bool
	// CookieDomain optionally scopes session cookies to a parent domain.
	CookieDomain string
	// TrustProxy allows X-Forwarded-For and X-Real-IP to determine the client address.
	// Enable it only when a proxy the operator controls strips inbound copies of those
	// headers, otherwise any caller can spoof its address past the rate limiters.
	TrustProxy bool

	// UsersEnabled turns the entire multi-user feature on. When false the user and
	// org routes are not mounted and only Server Admin authentication exists.
	UsersEnabled bool
	// RegistrationMode is one of open, invite, admin_only, disabled.
	RegistrationMode string
	// RequireEmailVerification blocks login until the address is confirmed.
	RequireEmailVerification bool
	// RequireApproval holds new accounts until a Server Admin approves them.
	RequireApproval bool
	// SessionTTL is how long a newly issued session cookie remains valid.
	SessionTTL time.Duration
	// MaxSessionsPerUser caps concurrent sessions; the oldest is evicted past the cap.
	MaxSessionsPerUser int
	// ExtraReservedNames are operator-supplied additions to the username blocklist.
	ExtraReservedNames []string

	// OrgsEnabled turns organizations on.
	OrgsEnabled bool
	// OrgCreationMode is one of open, invite, admin_only, disabled.
	OrgCreationMode string
	// MaxOrgsPerUser caps how many orgs a single user may own. Zero means unlimited.
	MaxOrgsPerUser int
	// MaxMembersPerOrg caps org membership. Zero means unlimited.
	MaxMembersPerOrg int

	// DomainsEnabled turns custom domains on.
	DomainsEnabled bool
	// DomainsRequireApproval holds a verified domain until a Server Admin activates it.
	DomainsRequireApproval bool
	// MaxDomainsPerOwner caps domains per user or org. Zero means unlimited.
	MaxDomainsPerOwner int
	// DomainVerificationPrefix is the label prepended for the ownership TXT record.
	DomainVerificationPrefix string
	// DomainVerificationTTL is how long an unverified domain may sit before cleanup.
	DomainVerificationTTL time.Duration
	// ReservedDomains can never be claimed by a tenant.
	ReservedDomains []string
	// AllowWildcards permits wildcard custom domains, which force the dns-01 challenge.
	AllowWildcards bool
}

// DefaultConfig returns the shipped defaults. First run works with zero configuration.
func DefaultConfig() Config {
	return Config{
		SiteName:                 "cashp",
		BaseURL:                  "http://localhost:8080",
		AdminPath:                "server",
		APIVersion:               "v1",
		Secure:                   true,
		UsersEnabled:             true,
		RegistrationMode:         RegistrationOpen,
		RequireEmailVerification: true,
		RequireApproval:          false,
		SessionTTL:               SessionTTL,
		MaxSessionsPerUser:       10,
		OrgsEnabled:              true,
		OrgCreationMode:          OrgCreationOpen,
		MaxOrgsPerUser:           0,
		MaxMembersPerOrg:         0,
		DomainsEnabled:           true,
		DomainsRequireApproval:   false,
		MaxDomainsPerOwner:       0,
		DomainVerificationPrefix: "_cashp-verify",
		DomainVerificationTTL:    7 * 24 * time.Hour,
		AllowWildcards:           false,
	}
}

// normalize fills in any zero value with its default so a partially populated
// Config from the caller can never produce a disabled-by-accident policy.
func (c *Config) normalize() {
	d := DefaultConfig()
	if c.SiteName == "" {
		c.SiteName = d.SiteName
	}
	if c.BaseURL == "" {
		c.BaseURL = d.BaseURL
	}
	if c.AdminPath == "" {
		c.AdminPath = d.AdminPath
	}
	if c.APIVersion == "" {
		c.APIVersion = d.APIVersion
	}
	if !validRegistrationMode(c.RegistrationMode) {
		c.RegistrationMode = d.RegistrationMode
	}
	if !validOrgCreationMode(c.OrgCreationMode) {
		c.OrgCreationMode = d.OrgCreationMode
	}
	if c.SessionTTL <= 0 {
		c.SessionTTL = d.SessionTTL
	}
	if c.MaxSessionsPerUser <= 0 {
		c.MaxSessionsPerUser = d.MaxSessionsPerUser
	}
	if c.DomainVerificationPrefix == "" {
		c.DomainVerificationPrefix = d.DomainVerificationPrefix
	}
	if c.DomainVerificationTTL <= 0 {
		c.DomainVerificationTTL = d.DomainVerificationTTL
	}
}

func validRegistrationMode(m string) bool {
	switch m {
	case RegistrationOpen, RegistrationInvite, RegistrationAdminOnly, RegistrationDisabled:
		return true
	}
	return false
}

func validOrgCreationMode(m string) bool {
	switch m {
	case OrgCreationOpen, OrgCreationInvite, OrgCreationAdminOnly, OrgCreationDisabled:
		return true
	}
	return false
}
