// Package hosting implements the tenant-facing hosting services named in
// IDEA.md "Business logic": nginx virtual hosts with multi-version PHP-FPM,
// BIND authoritative DNS, the Postfix/Dovecot/OpenDKIM/OpenDMARC mail stack,
// and PaaS application deploys driven through the container orchestrator.
//
// Every service in this package follows the same contract: tenant-supplied
// values are validated against a strict allowlist before they reach a
// template, generated configuration is written atomically and verified with
// the service's own config-check command before it is activated, and every
// filesystem write is confined to the tenant's own root through
// security.SafeJoin.
package hosting

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// Directory names under the hosting root. They are fixed so a generated
// config file always resolves to the same place on every node in a cluster.
const (
	// DirSites holds per-tenant document roots.
	DirSites = "sites"
	// DirApps holds per-tenant PaaS release trees.
	DirApps = "apps"
	// DirMail holds virtual mailbox storage.
	DirMail = "mail"
	// DirNginx holds generated nginx server blocks.
	DirNginx = "conf/nginx"
	// DirBind holds generated BIND zone files and the zone include file.
	DirBind = "conf/bind"
	// DirBindZones holds one generated zone file per DNS zone.
	DirBindZones = "conf/bind/zones"
	// DirBindKeys holds the BIND-managed DNSSEC key directory.
	DirBindKeys = "conf/bind/keys"
	// DirMailConf holds generated Postfix/Dovecot/OpenDKIM maps.
	DirMailConf = "conf/mail"
	// DirDKIM holds generated OpenDKIM signing keys.
	DirDKIM = "conf/dkim"
)

// File modes for generated artefacts. Configuration is group readable so the
// service user can read it; key material is owner-only.
const (
	configMode  os.FileMode = 0o640
	secretMode  os.FileMode = 0o600
	dirMode     os.FileMode = 0o750
	docRootMode os.FileMode = 0o755
)

// Runner executes a host command as an argv slice. Every implementation must
// exec the binary directly: a shell is never involved, so no tenant-supplied
// value can become a shell token.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Orchestrator is the narrow slice of src/orchestrator this package needs to
// run PaaS workloads. The concrete orchestrator satisfies it at wiring time.
type Orchestrator interface {
	Deploy(ctx context.Context, spec WorkloadSpec) (string, error)
	Start(ctx context.Context, workloadID string) error
	Stop(ctx context.Context, workloadID string) error
	Scale(ctx context.Context, workloadID string, replicas int) error
	Remove(ctx context.Context, workloadID string) error
	Logs(ctx context.Context, workloadID string, lines int) ([]string, error)
}

// DatabaseProvisioner is the narrow slice of src/dbservice this package needs
// to attach a managed database instance to a PaaS app.
type DatabaseProvisioner interface {
	EnsureInstance(ctx context.Context, tenantID, engine, name string) (DatabaseRef, error)
	RemoveInstance(ctx context.Context, tenantID, instanceID string) error
}

// PackageManager is the narrow slice of src/hostpkg this package needs to make
// sure the native service backing a feature is installed before it is used.
type PackageManager interface {
	EnsureInstalled(ctx context.Context, packages ...string) error
	ServiceUnit(service string) (string, bool)
}

// CertManager is the narrow slice of *tlsmgr.Manager used for site TLS.
type CertManager interface {
	AddDomain(ctx context.Context, domain string) error
	RemoveDomain(domain string) error
	CertificateFor(domain string) (*tls.Certificate, bool)
}

// QuotaProvider reports the billing-plan ceiling for a countable resource.
// A negative limit means unlimited; a nil provider disables quota checks so
// the package stays usable before billing is wired in.
type QuotaProvider interface {
	Limit(ctx context.Context, tenantID, resource string) (int64, error)
}

// Quota resource keys understood by QuotaProvider.
const (
	// ResourceSites limits how many sites a tenant may own.
	ResourceSites = "sites"
	// ResourceZones limits how many DNS zones a tenant may own.
	ResourceZones = "zones"
	// ResourceMailboxes limits how many mailboxes a tenant may own.
	ResourceMailboxes = "mailboxes"
	// ResourceApps limits how many PaaS apps a tenant may own.
	ResourceApps = "apps"
	// ResourceDiskMB limits total tenant disk usage in megabytes.
	ResourceDiskMB = "disk_mb"
)

// DatabaseRef identifies a managed database instance handed to a PaaS app.
type DatabaseRef struct {
	InstanceID string
	Engine     string
	Host       string
	Port       int
	Database   string
	Username   string
}

// WorkloadSpec describes a PaaS release for the orchestrator. Env carries
// decrypted values and must never be logged.
type WorkloadSpec struct {
	TenantID  string
	AppID     string
	ReleaseID string
	Name      string
	Runtime   string
	Image     string
	SourceDir string
	Command   []string
	Env       map[string]string
	Replicas  int
	Port      int
	MemoryMB  int64
	CPUShares int64
}

// Options configures a hosting Service.
type Options struct {
	// Root is the filesystem root every generated path lives under.
	Root string
	// Store persists hosting state.
	Store Store
	// Runner executes host commands; defaults to ExecRunner.
	Runner Runner
	// Certs wires site TLS through src/tlsmgr; optional.
	Certs CertManager
	// TLSDir is the tlsmgr data directory holding issued certificates.
	TLSDir string
	// Orchestrator runs PaaS workloads; required for PaaS operations.
	Orchestrator Orchestrator
	// Databases provisions managed databases for apps; optional.
	Databases DatabaseProvisioner
	// Packages ensures native service packages exist; optional.
	Packages PackageManager
	// Quotas reports billing-plan ceilings; optional.
	Quotas QuotaProvider
	// Prover verifies domain ownership; defaults to NetProver.
	Prover DomainProver
	// EncryptionKey is the 32-byte key protecting secret env vars and DKIM keys.
	EncryptionKey []byte
	// Commands overrides the service check/reload command set.
	Commands CommandSet
	// Nameservers are the authoritative NS hostnames used in generated zones.
	Nameservers []string
	// Hostmaster is the SOA responsible-party mailbox.
	Hostmaster string
	// DefaultPHPVersion is applied to a site that does not name one.
	DefaultPHPVersion string
	// PHPSocketDir holds the per-version PHP-FPM sockets; defaults to /run/php.
	PHPSocketDir string
	// MailHostname is the public hostname of the mail transport.
	MailHostname string
	// KeepReleases is how many superseded releases survive deploy cleanup.
	KeepReleases int
	// Now supplies the clock; defaults to time.Now.
	Now func() time.Time
	// NewID supplies identifiers; defaults to a random 16-byte hex string.
	NewID func() string
}

// Service is the hosting facade the admin panel, API layer, and billing call.
type Service struct {
	root         string
	store        Store
	runner       Runner
	certs        CertManager
	tlsDir       string
	orchestrator Orchestrator
	databases    DatabaseProvisioner
	packages     PackageManager
	quotas       QuotaProvider
	prover       DomainProver
	key          []byte
	cmds         CommandSet
	nameservers  []string
	hostmaster   string
	defaultPHP   string
	phpSocketDir string
	mailHostname string
	keepReleases int
	now          func() time.Time
	newID        func() string
}

// New validates the options and builds a Service. It never touches a host
// service: the first command runs only when a caller asks for a change.
func New(opts Options) (*Service, error) {
	if strings.TrimSpace(opts.Root) == "" {
		return nil, apperr.New(apperr.CodeInternal, 500, "hosting root is not configured")
	}
	if opts.Store == nil {
		return nil, apperr.New(apperr.CodeInternal, 500, "hosting store is not configured")
	}
	if len(opts.EncryptionKey) != 32 {
		return nil, apperr.New(apperr.CodeInternal, 500, "hosting encryption key is not configured")
	}

	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	newID := opts.NewID
	if newID == nil {
		newID = randomID
	}
	prover := opts.Prover
	if prover == nil {
		prover = NetProver{}
	}
	php := opts.DefaultPHPVersion
	if php == "" {
		php = PHPVersionNone
	}
	if err := ValidatePHPVersion(php); err != nil {
		return nil, err
	}
	socketDir := opts.PHPSocketDir
	if socketDir == "" {
		socketDir = defaultPHPSocketDir
	}
	if _, err := tmplToken(socketDir); err != nil {
		return nil, err
	}
	keep := opts.KeepReleases
	if keep <= 0 {
		keep = defaultKeepReleases
	}

	ns := make([]string, 0, len(opts.Nameservers))
	for _, n := range opts.Nameservers {
		host, err := ValidateDomain(n)
		if err != nil {
			return nil, err
		}
		ns = append(ns, host)
	}
	hostmaster := opts.Hostmaster
	if hostmaster == "" {
		hostmaster = "hostmaster"
	}
	if err := ValidateHostmaster(hostmaster); err != nil {
		return nil, err
	}
	mailHost := opts.MailHostname
	if mailHost != "" {
		normalized, err := ValidateDomain(mailHost)
		if err != nil {
			return nil, err
		}
		mailHost = normalized
	}

	key := make([]byte, len(opts.EncryptionKey))
	copy(key, opts.EncryptionKey)

	return &Service{
		root:         filepath.Clean(opts.Root),
		store:        opts.Store,
		runner:       runner,
		certs:        opts.Certs,
		tlsDir:       opts.TLSDir,
		orchestrator: opts.Orchestrator,
		databases:    opts.Databases,
		packages:     opts.Packages,
		quotas:       opts.Quotas,
		prover:       prover,
		key:          key,
		cmds:         opts.Commands.withDefaults(),
		nameservers:  ns,
		hostmaster:   hostmaster,
		defaultPHP:   php,
		phpSocketDir: socketDir,
		mailHostname: mailHost,
		keepReleases: keep,
		now:          now,
		newID:        newID,
	}, nil
}

// Root reports the filesystem root the service writes under.
func (s *Service) Root() string { return s.root }

// tenantDir resolves a tenant-scoped directory below one of the layout roots,
// always through security.SafeJoin so traversal is structurally impossible.
func (s *Service) tenantDir(area, tenantID string, parts ...string) (string, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return "", err
	}
	rel := path(area, tenantID)
	for _, p := range parts {
		if p == "" {
			continue
		}
		rel = path(rel, p)
	}
	joined, err := security.SafeJoin(s.root, rel)
	if err != nil {
		return "", apperr.Wrap(err, apperr.CodeBadRequest, 400, "invalid path")
	}
	return joined, nil
}

// systemPath resolves a non-tenant path (generated service config) under root.
func (s *Service) systemPath(parts ...string) (string, error) {
	rel := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		rel = path(rel, p)
	}
	joined, err := security.SafeJoin(s.root, rel)
	if err != nil {
		return "", apperr.Wrap(err, apperr.CodeBadRequest, 400, "invalid path")
	}
	return joined, nil
}

// path joins two relative path fragments with a forward slash.
func path(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "/" + b
	}
}

// ensureDir creates dir and its parents with the hosting directory mode.
func ensureDir(dir string, mode os.FileMode) error {
	if err := os.MkdirAll(dir, mode); err != nil {
		return apperr.Wrap(err, apperr.CodeInternal, 500, "storage is unavailable")
	}
	return nil
}

// checkQuota rejects a create when the tenant is already at its plan ceiling.
func (s *Service) checkQuota(ctx context.Context, tenantID, resource string, current int64) error {
	if s.quotas == nil {
		return nil
	}
	limit, err := s.quotas.Limit(ctx, tenantID, resource)
	if err != nil {
		return apperr.Wrap(err, apperr.CodeInternal, 500, "quota check failed")
	}
	if limit < 0 {
		return nil
	}
	if current >= limit {
		return apperr.New(apperr.CodeQuotaExceeded, 429, "plan limit reached for this resource").
			WithDetails(map[string]any{"resource": resource, "limit": limit})
	}
	return nil
}

// requireConfirm guards a destructive operation behind an explicit flag.
func requireConfirm(confirm bool) error {
	if confirm {
		return nil
	}
	return apperr.New(apperr.CodeBadRequest, 400, "confirmation is required for this operation")
}

// internalErr converts a host-side failure into an API-safe error. The cause
// is retained for logs only, never rendered into the response body.
func internalErr(err error, message string) *apperr.Error {
	return apperr.Wrap(err, apperr.CodeInternal, 500, message)
}

// notFound builds the generic not-found error used for every entity so a
// probe cannot distinguish "belongs to another tenant" from "does not exist".
func notFound(kind string) *apperr.Error {
	return apperr.New(apperr.CodeNotFound, 404, fmt.Sprintf("%s not found", kind))
}
