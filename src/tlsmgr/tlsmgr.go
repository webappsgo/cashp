// Package tlsmgr implements cashp's SSL/TLS layer per AI.md PART 15:
// automatic Let's Encrypt (ACME) issuance and renewal over HTTP-01 and
// TLS-ALPN-01, a multi-domain on-disk certificate store shared by the
// panel hostname, hosted vhosts, and verified tenant custom domains
// (PART 36), operator-supplied static certificates, and a self-signed
// fallback. Certificate provisioning never takes a site down: a failure
// keeps the existing certificate in service (or degrades to the
// self-signed fallback) and the scheduler's ssl_renewal task retries.
package tlsmgr

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/webappsgo/cashp/src/notify"
	"github.com/webappsgo/cashp/src/scheduler"
)

const (
	// renewBefore is the AI.md PART 15 renewal window: certificates are
	// renewed seven days before they expire.
	renewBefore = 7 * 24 * time.Hour

	// stagingDirectoryURL is Let's Encrypt's staging endpoint, used when
	// Options.Staging is set so rate limits are never burned during testing.
	stagingDirectoryURL = "https://acme-staging-v02.api.letsencrypt.org/directory"

	// obtainTimeout bounds a single certificate issuance attempt.
	obtainTimeout = 2 * time.Minute

	// httpChallengePath is the HTTP-01 well-known prefix.
	httpChallengePath = "/.well-known/acme-challenge/"

	// domainsFile persists domains added at runtime so tenant custom domains
	// survive a restart.
	domainsFile = "domains.json"
)

// Options configures a Manager.
type Options struct {
	// DataDir is the root under which {data_dir}/ssl/** is maintained.
	DataDir string

	// Email is the ACME account contact address. Optional but recommended:
	// Let's Encrypt uses it for expiry warnings.
	Email string

	// Domains is the initial domain set (the panel hostname plus any vhosts
	// known at startup).
	Domains []string

	// Staging directs issuance at Let's Encrypt's staging directory.
	Staging bool

	// HTTPPort is the plaintext listener port. Port 80 enables HTTP-01.
	HTTPPort int

	// HTTPSPort is the TLS listener port. Port 443 enables TLS-ALPN-01.
	HTTPSPort int

	// Enabled turns automatic ACME issuance on. When false the manager still
	// serves on-disk and self-signed certificates.
	Enabled bool

	// Notifier delivers ssl_expiring/ssl_renewed/ssl_renewal_failed
	// notifications per AI.md PART 18's decision matrix; nil disables
	// notification entirely.
	Notifier *notify.Notifier
}

// Manager owns the certificate store, the ACME client, and the domain set.
// It is safe for concurrent use.
type Manager struct {
	opts  Options
	store *certStore
	acme  *autocert.Manager
	log   *slog.Logger

	mu         sync.RWMutex
	domains    map[string]struct{}
	selfSigned map[string]*tls.Certificate
}

// New creates a Manager, preparing {data_dir}/ssl/** and, when ACME is
// usable, an autocert client bound to the on-disk store.
func New(opts Options) (*Manager, error) {
	store, err := newCertStore(opts.DataDir)
	if err != nil {
		return nil, err
	}

	m := &Manager{
		opts:       opts,
		store:      store,
		log:        slog.Default().With("component", "tlsmgr"),
		domains:    make(map[string]struct{}),
		selfSigned: make(map[string]*tls.Certificate),
	}

	for _, d := range opts.Domains {
		if h := NormalizeHost(d); h != "" && !IsOverlayHost(h) {
			m.domains[h] = struct{}{}
		}
	}

	for _, d := range m.loadPersistedDomains() {
		m.domains[d] = struct{}{}
	}

	if opts.Enabled {
		if !m.challengesPossible() {
			m.log.Warn("acme issuance disabled: neither port 80 (HTTP-01) nor port 443 (TLS-ALPN-01) is bound",
				"http_port", opts.HTTPPort, "https_port", opts.HTTPSPort)
		} else {
			m.acme = &autocert.Manager{
				Prompt:      autocert.AcceptTOS,
				Cache:       store,
				Email:       opts.Email,
				HostPolicy:  m.hostPolicy,
				RenewBefore: renewBefore,
			}
			if opts.Staging {
				m.acme.Client = &acme.Client{DirectoryURL: stagingDirectoryURL}
			}
		}
	}

	return m, nil
}

// challengesPossible reports whether at least one ACME challenge type can
// actually be answered with the configured ports.
func (m *Manager) challengesPossible() bool {
	return m.opts.HTTPPort == 80 || m.opts.HTTPSPort == 443
}

// httpChallengeEnabled reports whether HTTP-01 can be answered.
func (m *Manager) httpChallengeEnabled() bool {
	return m.acme != nil && m.opts.HTTPPort == 80
}

// tlsALPNChallengeEnabled reports whether TLS-ALPN-01 can be answered.
func (m *Manager) tlsALPNChallengeEnabled() bool {
	return m.acme != nil && m.opts.HTTPSPort == 443
}

// domainsFilePath is where the runtime domain set is persisted.
func (m *Manager) domainsFilePath() string {
	return filepath.Join(m.store.sslRoot(), domainsFile)
}

// loadPersistedDomains reads the runtime domain set written by AddDomain. A
// missing or corrupt file is not fatal — it degrades to the configured set.
func (m *Manager) loadPersistedDomains() []string {
	data, err := os.ReadFile(m.domainsFilePath())
	if err != nil {
		if !os.IsNotExist(err) {
			m.log.Warn("could not read persisted tls domain list", "error", err)
		}
		return nil
	}

	var stored []string
	if err := json.Unmarshal(data, &stored); err != nil {
		m.log.Warn("persisted tls domain list is unreadable, ignoring it", "error", err)
		return nil
	}

	out := make([]string, 0, len(stored))
	for _, d := range stored {
		if h := NormalizeHost(d); h != "" && !IsOverlayHost(h) {
			out = append(out, h)
		}
	}

	return out
}

// persistDomains writes the current domain set. The caller must hold m.mu.
func (m *Manager) persistDomains() error {
	list := make([]string, 0, len(m.domains))
	for d := range m.domains {
		list = append(list, d)
	}
	sort.Strings(list)

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	return writeFileAtomic(m.domainsFilePath(), data, keyFileMode)
}

// Domains returns the current certificate domain set, sorted.
func (m *Manager) Domains() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]string, 0, len(m.domains))
	for d := range m.domains {
		out = append(out, d)
	}
	sort.Strings(out)

	return out
}

// allowed reports whether domain is in the certificate set, honouring a
// wildcard entry such as *.example.com for its direct subdomains.
func (m *Manager) allowed(domain string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.domains[domain]; ok {
		return true
	}

	for entry := range m.domains {
		base, found := strings.CutPrefix(entry, "*.")
		if !found {
			continue
		}
		sub, isSub := strings.CutSuffix(domain, "."+base)
		if isSub && sub != "" && !strings.Contains(sub, ".") {
			return true
		}
	}

	return false
}

// hostPolicy is autocert's gate: cashp only ever requests certificates for
// domains it has been told about, and never for an overlay address.
func (m *Manager) hostPolicy(_ context.Context, host string) error {
	h := NormalizeHost(host)
	if h == "" {
		return errors.New("tlsmgr: empty host")
	}

	if IsOverlayHost(h) {
		return fmt.Errorf("tlsmgr: %s is an overlay address and is served over http only", h)
	}

	if !m.allowed(h) {
		return fmt.Errorf("tlsmgr: host %s is not in the certificate domain set", h)
	}

	return nil
}

// TLSConfig returns the hardened server TLS configuration: TLS 1.2 minimum
// with TLS 1.3 preferred, forward-secret AEAD cipher suites only for 1.2,
// and GetCertificate wired to the ACME/on-disk store.
func (m *Manager) TLSConfig() *tls.Config {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS13,
		CurvePreferences: []tls.CurveID{
			tls.X25519,
			tls.CurveP256,
			tls.CurveP384,
		},
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
		NextProtos:     []string{"h2", "http/1.1"},
		GetCertificate: m.getCertificate,
	}

	// TLS-ALPN-01 is answered inside the TLS handshake, so its protocol must
	// be offered first when port 443 is bound.
	if m.tlsALPNChallengeEnabled() {
		cfg.NextProtos = append([]string{acme.ALPNProto}, cfg.NextProtos...)
	}

	return cfg
}

// getCertificate resolves the certificate for one handshake. Order: a valid
// certificate on disk, then ACME issuance, then any expired certificate on
// disk, then the self-signed fallback — issuance failure never breaks the
// handshake.
func (m *Manager) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	host := NormalizeHost(hello.ServerName)
	if host == "" {
		return nil, errors.New("tlsmgr: handshake without a server name")
	}

	if IsOverlayHost(host) {
		return nil, fmt.Errorf("tlsmgr: refusing tls for overlay host %s", host)
	}

	// TLS-ALPN-01 handshakes must be answered by autocert itself.
	if m.acme != nil && len(hello.SupportedProtos) > 0 {
		for _, proto := range hello.SupportedProtos {
			if proto == acme.ALPNProto {
				return m.acme.GetCertificate(hello)
			}
		}
	}

	if cert, _, ok := m.store.best(host); ok && !certExpired(cert) {
		return cert, nil
	}

	if m.acme != nil && m.allowed(host) {
		cert, err := m.acme.GetCertificate(hello)
		if err == nil {
			return cert, nil
		}
		m.log.Warn("acme issuance failed, falling back to existing certificate material",
			"domain", host, "error", err)
	}

	// Existing certificate, even an expired one, beats dropping the site.
	if cert, _, ok := m.store.best(host); ok {
		return cert, nil
	}

	return m.selfSignedFor(host)
}

// certExpired reports whether a parsed certificate is outside its validity
// window.
func certExpired(cert *tls.Certificate) bool {
	if cert == nil || cert.Leaf == nil {
		return false
	}

	now := time.Now()

	return now.After(cert.Leaf.NotAfter) || now.Before(cert.Leaf.NotBefore)
}

// selfSignedFor returns (creating and persisting on first use) the
// self-signed fallback certificate for host.
func (m *Manager) selfSignedFor(host string) (*tls.Certificate, error) {
	m.mu.RLock()
	cached, ok := m.selfSigned[host]
	m.mu.RUnlock()
	if ok && !certExpired(cached) {
		return cached, nil
	}

	cert, certPEM, keyPEM, err := generateSelfSigned(host)
	if err != nil {
		return nil, err
	}

	if err := m.store.saveLocal(host, certPEM, keyPEM); err != nil {
		m.log.Warn("could not persist self-signed certificate", "domain", host, "error", err)
	}

	m.mu.Lock()
	m.selfSigned[host] = cert
	m.mu.Unlock()

	return cert, nil
}

// HTTPChallengeHandler wraps the normal handler with the HTTP-01 responder.
// Non-challenge requests always fall through to next — this never redirects
// to HTTPS, so Tor and I2P requests keep working unchanged.
func (m *Manager) HTTPChallengeHandler(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}

	if !m.httpChallengeEnabled() {
		return next
	}

	challenge := m.acme.HTTPHandler(next)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, httpChallengePath) {
			challenge.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// AddDomain registers a domain (a verified tenant custom domain or a new
// hosted vhost) and attempts issuance immediately. Issuance failure is
// reported as a warning, not an error: the scheduler retries it.
func (m *Manager) AddDomain(ctx context.Context, domain string) error {
	host := NormalizeHost(domain)
	if host == "" {
		return errors.New("tlsmgr: domain is required")
	}

	if IsOverlayHost(host) {
		return fmt.Errorf("tlsmgr: %s is an overlay address and never gets a certificate", host)
	}

	m.mu.Lock()
	if _, exists := m.domains[host]; exists {
		m.mu.Unlock()
		return nil
	}
	m.domains[host] = struct{}{}
	err := m.persistDomains()
	m.mu.Unlock()

	if err != nil {
		return err
	}

	if certErr := m.obtain(ctx, host); certErr != nil {
		m.log.Warn("initial certificate issuance failed, will retry on the ssl_renewal schedule",
			"domain", host, "error", certErr)
	}

	return nil
}

// RemoveDomain drops a domain from the certificate set and deletes the
// certificate material cashp manages for it. Operator-supplied certificates
// under ssl/local are left alone.
func (m *Manager) RemoveDomain(domain string) error {
	host := NormalizeHost(domain)
	if host == "" {
		return errors.New("tlsmgr: domain is required")
	}

	m.mu.Lock()
	delete(m.domains, host)
	delete(m.selfSigned, host)
	err := m.persistDomains()
	m.mu.Unlock()

	if err != nil {
		return err
	}

	if rmErr := os.RemoveAll(m.store.managedDir(host)); rmErr != nil && !os.IsNotExist(rmErr) {
		return rmErr
	}

	return nil
}

// obtain requests a certificate for one domain through autocert, bounded by
// ctx and by obtainTimeout.
func (m *Manager) obtain(ctx context.Context, domain string) error {
	if m.acme == nil {
		return errors.New("tlsmgr: automatic issuance is disabled")
	}

	if err := m.hostPolicy(ctx, domain); err != nil {
		return err
	}

	reqCtx, cancel := context.WithTimeout(ctx, obtainTimeout)
	defer cancel()

	type result struct {
		err error
	}
	done := make(chan result, 1)

	go func() {
		_, err := m.acme.GetCertificate(clientHelloFor(domain))
		done <- result{err: err}
	}()

	select {
	case <-reqCtx.Done():
		return reqCtx.Err()
	case res := <-done:
		return res.err
	}
}

// clientHelloFor builds the synthetic handshake autocert needs to issue a
// certificate outside of a live connection. The advertised algorithms select
// an ECDSA key.
func clientHelloFor(domain string) *tls.ClientHelloInfo {
	return &tls.ClientHelloInfo{
		ServerName:        domain,
		SupportedVersions: []uint16{tls.VersionTLS13, tls.VersionTLS12},
		SignatureSchemes:  []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256, tls.PSSWithSHA256},
		SupportedCurves:   []tls.CurveID{tls.X25519, tls.CurveP256},
		CipherSuites:      []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
	}
}

// needsRenewal reports whether the app-managed certificate for domain is
// missing or inside the seven-day renewal window.
func (m *Manager) needsRenewal(domain string) bool {
	cert, ok := m.store.managedCertificate(domain)
	if !ok || cert.Leaf == nil {
		return true
	}

	return time.Now().Add(renewBefore).After(cert.Leaf.NotAfter)
}

// Renew is the scheduler's ssl_renewal task. It issues or renews every
// domain cashp manages, skipping certbot-owned and operator-owned material.
// A returned error is a report, never a reason to stop serving: every site
// keeps its current certificate and the next run retries.
func (m *Manager) Renew(ctx context.Context) error {
	var failures []error

	for _, domain := range m.Domains() {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}

		if m.store.hasSystemCertificate(domain) {
			// certbot owns /etc/letsencrypt/live/** renewal.
			continue
		}

		if !m.needsRenewal(domain) {
			continue
		}

		expiresIn, expiryDate := m.expiryInfo(domain)
		m.notifyExpiring(ctx, domain, expiresIn, expiryDate)

		if m.store.hasLocalCertificate(domain) && m.acme == nil {
			// Operator-supplied certificate, renewed by hand only.
			continue
		}

		if err := m.obtain(ctx, domain); err != nil {
			m.log.Warn("certificate renewal failed, existing certificate stays in service",
				"domain", domain, "error", err)
			failures = append(failures, fmt.Errorf("%s: %w", domain, err))
			m.notifyRenewalFailed(ctx, domain, err, expiresIn, expiryDate)

			continue
		}

		m.notifyRenewed(ctx, domain)
	}

	if len(failures) == 0 {
		return nil
	}

	return errors.Join(failures...)
}

// expiryInfo returns the whole days remaining and the absolute expiry date
// of domain's current app-managed certificate, for use in notification
// variables. A domain with no certificate on disk yet reports zero days.
func (m *Manager) expiryInfo(domain string) (daysLeft int, expiryDate string) {
	cert, ok := m.store.managedCertificate(domain)
	if !ok || cert.Leaf == nil {
		return 0, ""
	}

	remaining := time.Until(cert.Leaf.NotAfter)
	daysLeft = int(remaining.Hours() / 24)
	if daysLeft < 0 {
		daysLeft = 0
	}

	return daysLeft, cert.Leaf.NotAfter.Format("2006-01-02")
}

// notify dispatches one tlsmgr event, tolerating both an absent notifier and
// a delivery failure - a notification is never allowed to fail the renewal
// it describes.
func (m *Manager) notify(ctx context.Context, event string, vars map[string]string) {
	if m.opts.Notifier == nil {
		return
	}
	if err := m.opts.Notifier.Notify(ctx, notify.Message{Event: event, Vars: vars}); err != nil {
		m.log.Warn("tlsmgr notification failed", "event", event, "error", err)
	}
}

// notifyExpiring dispatches ssl_expiring for a domain entering its renewal
// window, ahead of the renewal attempt itself.
func (m *Manager) notifyExpiring(ctx context.Context, domain string, expiresIn int, expiryDate string) {
	m.notify(ctx, notify.EventSSLExpiring, map[string]string{
		"fqdn":        domain,
		"expires_in":  strconv.Itoa(expiresIn),
		"expiry_date": expiryDate,
	})
}

// notifyRenewed dispatches ssl_renewed after a successful renewal.
func (m *Manager) notifyRenewed(ctx context.Context, domain string) {
	_, validUntil := m.expiryInfo(domain)
	m.notify(ctx, notify.EventSSLRenewed, map[string]string{
		"fqdn":        domain,
		"valid_until": validUntil,
	})
}

// notifyRenewalFailed dispatches ssl_renewal_failed after a failed renewal
// attempt, using the ExecutionID so it can suppress scheduler_error for the
// same scheduled run per AI.md PART 18.
func (m *Manager) notifyRenewalFailed(ctx context.Context, domain string, renewErr error, expiresIn int, expiryDate string) {
	if m.opts.Notifier == nil {
		return
	}

	msg := notify.Message{
		Event:       notify.EventSSLRenewalFailed,
		ExecutionID: scheduler.ExecutionIDFromContext(ctx),
		Vars: map[string]string{
			"fqdn":        domain,
			"error":       renewErr.Error(),
			"expires_in":  strconv.Itoa(expiresIn),
			"expiry_date": expiryDate,
			"next_retry":  "next scheduled ssl_renewal run",
		},
	}

	if err := m.opts.Notifier.Notify(ctx, msg); err != nil {
		m.log.Warn("tlsmgr notification failed", "event", msg.Event, "error", err)
	}
}

// CertificateFor returns the certificate cashp would serve for domain
// without contacting an ACME server. It reports false when nothing usable
// exists on disk yet.
func (m *Manager) CertificateFor(domain string) (*tls.Certificate, bool) {
	host := NormalizeHost(domain)
	if host == "" || IsOverlayHost(host) {
		return nil, false
	}

	if cert, _, ok := m.store.best(host); ok {
		return cert, true
	}

	m.mu.RLock()
	cached, ok := m.selfSigned[host]
	m.mu.RUnlock()
	if ok && !certExpired(cached) {
		return cached, true
	}

	return nil, false
}
