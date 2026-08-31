package hosting

import (
	"context"
	"time"
)

// Site is one nginx virtual host owned by a tenant. DocRoot is stored as a
// path relative to the tenant's site directory and is always re-resolved
// through security.SafeJoin before use.
type Site struct {
	ID               string
	TenantID         string
	Name             string
	PrimaryDomain    string
	Aliases          []string
	DocRoot          string
	PHPVersion       string
	TLSEnabled       bool
	Enabled          bool
	DiskQuotaMB      int64
	BandwidthQuotaMB int64
	DiskUsedMB       int64
	GitRemote        string
	GitBranch        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Zone is a BIND authoritative zone owned by a tenant.
type Zone struct {
	ID         string
	TenantID   string
	Name       string
	PrimaryNS  string
	Hostmaster string
	Serial     int64
	Refresh    int64
	Retry      int64
	Expire     int64
	Minimum    int64
	DefaultTTL int64
	DNSSEC     bool
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Record is one resource record inside a zone. Priority carries the MX
// preference, the SRV priority, and the CAA flags byte depending on Type.
type Record struct {
	ID        string
	ZoneID    string
	TenantID  string
	Name      string
	Type      string
	Value     string
	TTL       int64
	Priority  int64
	Weight    int64
	Port      int64
	Managed   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MailDomain is a mail-hosting domain with its OpenDKIM signing key. The
// private key is stored encrypted and is never returned to a caller.
type MailDomain struct {
	ID           string
	TenantID     string
	Domain       string
	DKIMSelector string
	DKIMPrivate  []byte
	DKIMPublic   string
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Mailbox is a virtual mailbox. PasswordHash is an Argon2id PHC string
// produced by src/security; a plaintext password is never persisted.
type Mailbox struct {
	ID           string
	TenantID     string
	DomainID     string
	Domain       string
	LocalPart    string
	PasswordHash string
	QuotaMB      int64
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Alias forwards one address to another inside a mail domain.
type Alias struct {
	ID          string
	TenantID    string
	DomainID    string
	Domain      string
	Source      string
	Destination string
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// App is a PaaS application owned by a tenant.
type App struct {
	ID          string
	TenantID    string
	Name        string
	Runtime     string
	GitRemote   string
	GitBranch   string
	Domain      string
	Port        int
	Replicas    int
	MemoryMB    int64
	CPUShares   int64
	State       string
	WorkloadID  string
	ReleaseID   string
	DatabaseRef string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// EnvVar is one environment entry of an app. A secret value is stored as
// AES-GCM ciphertext and is masked on every read path except deployment.
type EnvVar struct {
	TenantID  string
	AppID     string
	Key       string
	Value     string
	Encrypted []byte
	Secret    bool
	UpdatedAt time.Time
}

// Release is one immutable deploy attempt of an app.
type Release struct {
	ID         string
	TenantID   string
	AppID      string
	Number     int64
	Source     string
	Image      string
	Command    string
	State      string
	WorkloadID string
	Log        string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// DomainOwnership records that a tenant proved control of a domain. A vhost
// or a zone for a domain is only ever activated once ownership is verified.
type DomainOwnership struct {
	Domain     string
	TenantID   string
	Token      string
	Method     string
	Verified   bool
	VerifiedAt time.Time
	CreatedAt  time.Time
}

// Release states. A release moves pending -> building -> deployed, or to
// failed; a deployed release becomes superseded when a newer one takes over
// and rolled_back when a caller returns to an earlier release.
const (
	ReleasePending    = "pending"
	ReleaseBuilding   = "building"
	ReleaseDeployed   = "deployed"
	ReleaseFailed     = "failed"
	ReleaseSuperseded = "superseded"
	ReleaseRolledBack = "rolled_back"
)

// App states reported to the panel.
const (
	AppCreated   = "created"
	AppRunning   = "running"
	AppStopped   = "stopped"
	AppDeploying = "deploying"
	AppFailed    = "failed"
)

// Domain verification methods.
const (
	VerifyDNS  = "dns"
	VerifyHTTP = "http"
)

// Store persists every hosting entity. It is an interface so the service is
// unit-testable without a database driver; SQLStore is the production
// implementation and enforces tenant scoping in SQL as well.
type Store interface {
	CreateSite(ctx context.Context, s Site) error
	UpdateSite(ctx context.Context, s Site) error
	GetSite(ctx context.Context, tenantID, id string) (Site, error)
	ListSites(ctx context.Context, tenantID string) ([]Site, error)
	ListAllSites(ctx context.Context) ([]Site, error)
	DeleteSite(ctx context.Context, tenantID, id string) error
	SiteByDomain(ctx context.Context, domain string) (Site, error)

	CreateZone(ctx context.Context, z Zone) error
	UpdateZone(ctx context.Context, z Zone) error
	GetZone(ctx context.Context, tenantID, id string) (Zone, error)
	ZoneByName(ctx context.Context, name string) (Zone, error)
	ListZones(ctx context.Context, tenantID string) ([]Zone, error)
	ListAllZones(ctx context.Context) ([]Zone, error)
	DeleteZone(ctx context.Context, tenantID, id string) error

	CreateRecord(ctx context.Context, r Record) error
	UpdateRecord(ctx context.Context, r Record) error
	GetRecord(ctx context.Context, tenantID, id string) (Record, error)
	ListRecords(ctx context.Context, tenantID, zoneID string) ([]Record, error)
	DeleteRecord(ctx context.Context, tenantID, id string) error

	CreateMailDomain(ctx context.Context, d MailDomain) error
	UpdateMailDomain(ctx context.Context, d MailDomain) error
	GetMailDomain(ctx context.Context, tenantID, id string) (MailDomain, error)
	MailDomainByName(ctx context.Context, domain string) (MailDomain, error)
	ListMailDomains(ctx context.Context, tenantID string) ([]MailDomain, error)
	ListAllMailDomains(ctx context.Context) ([]MailDomain, error)
	DeleteMailDomain(ctx context.Context, tenantID, id string) error

	CreateMailbox(ctx context.Context, m Mailbox) error
	UpdateMailbox(ctx context.Context, m Mailbox) error
	GetMailbox(ctx context.Context, tenantID, id string) (Mailbox, error)
	ListMailboxes(ctx context.Context, tenantID, domainID string) ([]Mailbox, error)
	ListAllMailboxes(ctx context.Context) ([]Mailbox, error)
	DeleteMailbox(ctx context.Context, tenantID, id string) error

	CreateAlias(ctx context.Context, a Alias) error
	GetAlias(ctx context.Context, tenantID, id string) (Alias, error)
	ListAliases(ctx context.Context, tenantID, domainID string) ([]Alias, error)
	ListAllAliases(ctx context.Context) ([]Alias, error)
	DeleteAlias(ctx context.Context, tenantID, id string) error

	CreateApp(ctx context.Context, a App) error
	UpdateApp(ctx context.Context, a App) error
	GetApp(ctx context.Context, tenantID, id string) (App, error)
	ListApps(ctx context.Context, tenantID string) ([]App, error)
	ListAllApps(ctx context.Context) ([]App, error)
	DeleteApp(ctx context.Context, tenantID, id string) error

	PutEnv(ctx context.Context, e EnvVar) error
	ListEnv(ctx context.Context, tenantID, appID string) ([]EnvVar, error)
	DeleteEnv(ctx context.Context, tenantID, appID, key string) error

	CreateRelease(ctx context.Context, r Release) error
	UpdateRelease(ctx context.Context, r Release) error
	GetRelease(ctx context.Context, tenantID, id string) (Release, error)
	ListReleases(ctx context.Context, tenantID, appID string) ([]Release, error)
	DeleteRelease(ctx context.Context, tenantID, id string) error

	PutOwnership(ctx context.Context, o DomainOwnership) error
	GetOwnership(ctx context.Context, domain string) (DomainOwnership, error)
	ListOwnership(ctx context.Context, tenantID string) ([]DomainOwnership, error)
}
