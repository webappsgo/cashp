package tlsmgr

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/acme"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/notify"
	"github.com/webappsgo/cashp/src/scheduler"
)

// newTestNotifier builds a real, SQLite-backed Notifier so dispatch can be
// asserted through its own dedup store, matching the pattern used by the
// backup, update, and scheduler packages.
func newTestNotifier(t *testing.T) *notify.Notifier {
	t.Helper()

	db, err := database.Open(database.Config{Driver: database.DriverSQLite, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	n, err := notify.New(notify.Options{DB: db, ConfigDir: t.TempDir(), AppName: "cashp"})
	if err != nil {
		t.Fatalf("new notifier: %v", err)
	}
	return n
}

// newTestManager builds a manager rooted in a temp dir with ACME issuance
// off, so no test can ever reach a real ACME server.
func newTestManager(t *testing.T, domains ...string) *Manager {
	t.Helper()

	dir := t.TempDir()
	m, err := New(Options{
		DataDir:   dir,
		Email:     "ops@example.com",
		Domains:   domains,
		HTTPPort:  8080,
		HTTPSPort: 8443,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.store.systemDir = filepath.Join(dir, "etc-letsencrypt-live")

	return m
}

func TestTLSConfigHardening(t *testing.T) {
	m := newTestManager(t, "panel.example.com")

	cfg := m.TLSConfig()
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %#x, want TLS 1.2", cfg.MinVersion)
	}
	if cfg.MaxVersion != tls.VersionTLS13 {
		t.Errorf("MaxVersion = %#x, want TLS 1.3", cfg.MaxVersion)
	}
	if cfg.GetCertificate == nil {
		t.Fatal("GetCertificate is not wired up")
	}

	want := []uint16{
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
	}
	allowed := make(map[uint16]bool, len(want))
	for _, suite := range want {
		allowed[suite] = true
	}

	if len(cfg.CipherSuites) != len(want) {
		t.Errorf("cipher suite count = %d, want %d", len(cfg.CipherSuites), len(want))
	}
	for _, suite := range cfg.CipherSuites {
		if !allowed[suite] {
			t.Errorf("unexpected cipher suite %#x", suite)
		}
	}

	for _, insecure := range tls.InsecureCipherSuites() {
		for _, suite := range cfg.CipherSuites {
			if suite == insecure.ID {
				t.Errorf("insecure cipher suite %s is enabled", insecure.Name)
			}
		}
	}

	if len(cfg.CurvePreferences) == 0 || cfg.CurvePreferences[0] != tls.X25519 {
		t.Errorf("CurvePreferences = %v, want X25519 first", cfg.CurvePreferences)
	}

	for _, proto := range cfg.NextProtos {
		if proto == acme.ALPNProto {
			t.Error("acme-tls/1 offered although port 443 is not bound")
		}
	}
}

func TestTLSConfigOffersALPNChallengeOnPort443(t *testing.T) {
	m, err := New(Options{DataDir: t.TempDir(), Domains: []string{"panel.example.com"}, HTTPPort: 80, HTTPSPort: 443, Enabled: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if m.acme == nil {
		t.Fatal("acme manager not created although port 80 and 443 are bound")
	}
	if !m.httpChallengeEnabled() || !m.tlsALPNChallengeEnabled() {
		t.Error("both HTTP-01 and TLS-ALPN-01 should be available")
	}

	cfg := m.TLSConfig()
	if cfg.NextProtos[0] != acme.ALPNProto {
		t.Errorf("NextProtos = %v, want acme-tls/1 first", cfg.NextProtos)
	}
}

func TestACMEDisabledWithoutUsablePorts(t *testing.T) {
	m, err := New(Options{DataDir: t.TempDir(), Domains: []string{"panel.example.com"}, HTTPPort: 8080, HTTPSPort: 8443, Enabled: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if m.acme != nil {
		t.Error("acme manager created although neither port 80 nor 443 is bound")
	}
}

func TestGetCertificateSelfSignedFallback(t *testing.T) {
	m := newTestManager(t, "panel.example.com")

	cert, err := m.TLSConfig().GetCertificate(clientHelloFor("panel.example.com"))
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cert.Leaf == nil {
		t.Fatal("returned certificate has no parsed leaf")
	}
	if err := cert.Leaf.VerifyHostname("panel.example.com"); err != nil {
		t.Errorf("self-signed certificate does not cover the host: %v", err)
	}

	info, err := os.Stat(filepath.Join(m.store.localDir("panel.example.com"), "key.pem"))
	if err != nil {
		t.Fatalf("self-signed key was not persisted: %v", err)
	}
	if perm := info.Mode().Perm(); perm != keyFileMode {
		t.Errorf("self-signed key mode = %o, want %o", perm, keyFileMode)
	}

	if _, ok := m.CertificateFor("panel.example.com"); !ok {
		t.Error("CertificateFor did not find the fallback certificate")
	}
}

func TestGetCertificateRefusesOverlayHosts(t *testing.T) {
	m := newTestManager(t)

	if _, err := m.getCertificate(clientHelloFor("abcdefghij.onion")); err == nil {
		t.Error("getCertificate served TLS for a .onion host")
	}
	if _, err := m.getCertificate(clientHelloFor("")); err == nil {
		t.Error("getCertificate accepted an empty server name")
	}
	if _, ok := m.CertificateFor("xyz.b32.i2p"); ok {
		t.Error("CertificateFor returned a certificate for an I2P host")
	}
}

func TestGetCertificatePrefersExistingDiskCertificate(t *testing.T) {
	m := newTestManager(t, "site.example.com")

	now := time.Now()
	certPEM, keyPEM := testPair(t, "site.example.com", now.Add(-time.Hour), now.Add(90*24*time.Hour))
	if err := m.store.Put(context.Background(), "site.example.com", append(append([]byte{}, keyPEM...), certPEM...)); err != nil {
		t.Fatalf("seed managed certificate: %v", err)
	}

	cert, err := m.getCertificate(clientHelloFor("site.example.com"))
	if err != nil {
		t.Fatalf("getCertificate: %v", err)
	}
	if cert.Leaf.Subject.CommonName != "site.example.com" {
		t.Errorf("served CN = %q, want site.example.com", cert.Leaf.Subject.CommonName)
	}

	// The stored certificate is nowhere near expiry, so no renewal is due.
	if m.needsRenewal("site.example.com") {
		t.Error("needsRenewal = true for a certificate valid for 90 more days")
	}
}

func TestNeedsRenewalInsideWindow(t *testing.T) {
	m := newTestManager(t, "soon.example.com")

	if !m.needsRenewal("soon.example.com") {
		t.Error("needsRenewal = false although no certificate exists yet")
	}

	now := time.Now()
	certPEM, keyPEM := testPair(t, "soon.example.com", now.Add(-time.Hour), now.Add(3*24*time.Hour))
	if err := m.store.Put(context.Background(), "soon.example.com", append(append([]byte{}, keyPEM...), certPEM...)); err != nil {
		t.Fatalf("seed managed certificate: %v", err)
	}

	if !m.needsRenewal("soon.example.com") {
		t.Error("needsRenewal = false for a certificate expiring in three days")
	}
}

func TestAddAndRemoveDomainPersist(t *testing.T) {
	dir := t.TempDir()
	m, err := New(Options{DataDir: dir, Domains: []string{"panel.example.com"}, HTTPPort: 8080})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if err := m.AddDomain(ctx, "Tenant.Example.NET"); err != nil {
		t.Fatalf("AddDomain: %v", err)
	}
	if err := m.AddDomain(ctx, "tenant.example.net"); err != nil {
		t.Fatalf("AddDomain (duplicate): %v", err)
	}

	if err := m.AddDomain(ctx, "abcdefghij.onion"); err == nil {
		t.Error("AddDomain accepted an overlay address")
	}
	if err := m.AddDomain(ctx, "  "); err == nil {
		t.Error("AddDomain accepted an empty domain")
	}

	reloaded, err := New(Options{DataDir: dir, HTTPPort: 8080})
	if err != nil {
		t.Fatalf("New (reload): %v", err)
	}
	got := reloaded.Domains()
	if len(got) != 2 || got[0] != "panel.example.com" || got[1] != "tenant.example.net" {
		t.Fatalf("reloaded domains = %v, want [panel.example.com tenant.example.net]", got)
	}

	now := time.Now()
	certPEM, keyPEM := testPair(t, "tenant.example.net", now.Add(-time.Hour), now.Add(48*time.Hour))
	if err := m.store.Put(ctx, "tenant.example.net", append(append([]byte{}, keyPEM...), certPEM...)); err != nil {
		t.Fatalf("seed managed certificate: %v", err)
	}

	if err := m.RemoveDomain("tenant.example.net"); err != nil {
		t.Fatalf("RemoveDomain: %v", err)
	}
	if _, err := os.Stat(m.store.managedDir("tenant.example.net")); !os.IsNotExist(err) {
		t.Error("RemoveDomain left managed certificate material on disk")
	}
	if m.allowed("tenant.example.net") {
		t.Error("removed domain is still allowed")
	}
	if err := m.RemoveDomain(""); err == nil {
		t.Error("RemoveDomain accepted an empty domain")
	}
}

func TestHostPolicyWildcardAndUnknownHost(t *testing.T) {
	m := newTestManager(t, "*.example.com", "panel.example.org")

	ctx := context.Background()
	if err := m.hostPolicy(ctx, "app.example.com"); err != nil {
		t.Errorf("wildcard entry did not cover a subdomain: %v", err)
	}
	if err := m.hostPolicy(ctx, "deep.app.example.com"); err == nil {
		t.Error("wildcard entry wrongly covered a second-level subdomain")
	}
	if err := m.hostPolicy(ctx, "panel.example.org"); err != nil {
		t.Errorf("exact entry rejected: %v", err)
	}
	if err := m.hostPolicy(ctx, "attacker.example.net"); err == nil {
		t.Error("hostPolicy allowed a host outside the domain set")
	}
	if err := m.hostPolicy(ctx, "abcdefghij.onion"); err == nil {
		t.Error("hostPolicy allowed an overlay address")
	}
	if err := m.hostPolicy(ctx, ""); err == nil {
		t.Error("hostPolicy allowed an empty host")
	}
}

func TestRenewWithoutACMEKeepsServingAndReportsFailure(t *testing.T) {
	m := newTestManager(t, "site.example.com")

	now := time.Now()
	certPEM, keyPEM := testPair(t, "site.example.com", now.Add(-30*24*time.Hour), now.Add(2*24*time.Hour))
	if err := m.store.Put(context.Background(), "site.example.com", append(append([]byte{}, keyPEM...), certPEM...)); err != nil {
		t.Fatalf("seed managed certificate: %v", err)
	}

	err := m.Renew(context.Background())
	if err == nil {
		t.Fatal("Renew reported success although issuance is disabled")
	}

	// Degradation is graceful: the existing certificate still serves.
	cert, ok := m.CertificateFor("site.example.com")
	if !ok {
		t.Fatal("existing certificate disappeared after a failed renewal")
	}
	if certExpired(cert) {
		t.Error("served certificate is expired")
	}

	served, getErr := m.getCertificate(clientHelloFor("site.example.com"))
	if getErr != nil {
		t.Fatalf("handshake failed after a failed renewal: %v", getErr)
	}
	if served.Leaf.Subject.CommonName != "site.example.com" {
		t.Errorf("served CN = %q, want site.example.com", served.Leaf.Subject.CommonName)
	}
}

func TestRenewWithoutNotifierSkipsNotification(t *testing.T) {
	m := newTestManager(t, "site.example.com")

	now := time.Now()
	certPEM, keyPEM := testPair(t, "site.example.com", now.Add(-30*24*time.Hour), now.Add(2*24*time.Hour))
	if err := m.store.Put(context.Background(), "site.example.com", append(append([]byte{}, keyPEM...), certPEM...)); err != nil {
		t.Fatalf("seed managed certificate: %v", err)
	}

	if err := m.Renew(context.Background()); err == nil {
		t.Fatal("Renew reported success although issuance is disabled")
	}
}

func TestRenewNotifiesSSLExpiringAndRenewalFailed(t *testing.T) {
	m := newTestManager(t, "site.example.com")
	m.opts.Notifier = newTestNotifier(t)

	now := time.Now()
	certPEM, keyPEM := testPair(t, "site.example.com", now.Add(-30*24*time.Hour), now.Add(2*24*time.Hour))
	if err := m.store.Put(context.Background(), "site.example.com", append(append([]byte{}, keyPEM...), certPEM...)); err != nil {
		t.Fatalf("seed managed certificate: %v", err)
	}

	ctx := scheduler.WithExecutionID(context.Background(), "test-run")
	if err := m.Renew(ctx); err == nil {
		t.Fatal("Renew reported success although issuance is disabled")
	}

	expiringHeld, err := m.opts.Notifier.Store().DedupHeld(ctx, notify.EventSSLExpiring+":")
	if err != nil {
		t.Fatalf("dedup held: %v", err)
	}
	if !expiringHeld {
		t.Error("expected ssl_expiring to have been dispatched")
	}

	failedHeld, err := m.opts.Notifier.Store().DedupHeld(ctx, notify.EventSSLRenewalFailed+":test-run")
	if err != nil {
		t.Fatalf("dedup held: %v", err)
	}
	if !failedHeld {
		t.Error("expected ssl_renewal_failed to have been dispatched with the run's ExecutionID")
	}
}

func TestNotifyRenewedDispatchesSSLRenewed(t *testing.T) {
	m := newTestManager(t, "site.example.com")
	m.opts.Notifier = newTestNotifier(t)

	now := time.Now()
	certPEM, keyPEM := testPair(t, "site.example.com", now.Add(-time.Hour), now.Add(60*24*time.Hour))
	if err := m.store.Put(context.Background(), "site.example.com", append(append([]byte{}, keyPEM...), certPEM...)); err != nil {
		t.Fatalf("seed managed certificate: %v", err)
	}

	ctx := context.Background()
	m.notifyRenewed(ctx, "site.example.com")

	held, err := m.opts.Notifier.Store().DedupHeld(ctx, notify.EventSSLRenewed+":")
	if err != nil {
		t.Fatalf("dedup held: %v", err)
	}
	if !held {
		t.Fatal("expected ssl_renewed to have been dispatched")
	}
}

func TestRenewSkipsSystemAndFreshCertificates(t *testing.T) {
	m := newTestManager(t, "system.example.com", "fresh.example.com")

	now := time.Now()
	sysDir := filepath.Join(m.store.systemDir, "system.example.com")
	sysCert, sysKey := testPair(t, "system.example.com", now.Add(-time.Hour), now.Add(30*24*time.Hour))
	if err := writeFileAtomic(filepath.Join(sysDir, "fullchain.pem"), sysCert, certFileMode); err != nil {
		t.Fatalf("write system cert: %v", err)
	}
	if err := writeFileAtomic(filepath.Join(sysDir, "privkey.pem"), sysKey, keyFileMode); err != nil {
		t.Fatalf("write system key: %v", err)
	}

	freshCert, freshKey := testPair(t, "fresh.example.com", now.Add(-time.Hour), now.Add(60*24*time.Hour))
	if err := m.store.Put(context.Background(), "fresh.example.com", append(append([]byte{}, freshKey...), freshCert...)); err != nil {
		t.Fatalf("seed managed certificate: %v", err)
	}

	if err := m.Renew(context.Background()); err != nil {
		t.Errorf("Renew = %v, want nil (nothing was due)", err)
	}
}

func TestRenewHonoursCancelledContext(t *testing.T) {
	m := newTestManager(t, "site.example.com")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := m.Renew(ctx); err == nil {
		t.Error("Renew ignored a cancelled context")
	}
}

func TestHTTPChallengeHandlerPassesTrafficThrough(t *testing.T) {
	m := newTestManager(t, "panel.example.com")

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	// With HTTP-01 unavailable the handler is returned untouched.
	rec := httptest.NewRecorder()
	m.HTTPChallengeHandler(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://panel.example.com/", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}

	acmeManager, err := New(Options{DataDir: t.TempDir(), Domains: []string{"panel.example.com"}, HTTPPort: 80, HTTPSPort: 443, Enabled: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	handler := acmeManager.HTTPChallengeHandler(next)

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://panel.example.com/dashboard", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("non-challenge request status = %d, want %d (no redirect allowed)", rec.Code, http.StatusTeapot)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://abcdefghij.onion/dashboard", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("overlay request status = %d, want %d (never redirected to https)", rec.Code, http.StatusTeapot)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://panel.example.com"+httpChallengePath+"unknown-token", nil))
	if rec.Code == http.StatusTeapot {
		t.Error("challenge path was not handled by the ACME responder")
	}
}

func TestNewRejectsEmptyDataDir(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Error("New accepted an empty data dir")
	}
}

func TestNewIgnoresOverlayDomainsInOptions(t *testing.T) {
	m := newTestManager(t, "panel.example.com", "abcdefghij.onion", "xyz.b32.i2p", "")

	got := m.Domains()
	if len(got) != 1 || got[0] != "panel.example.com" {
		t.Errorf("Domains() = %v, want [panel.example.com]", got)
	}
}
