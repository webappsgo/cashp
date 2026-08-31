package tlsmgr

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// testPair builds a certificate/key PEM pair for domain with an explicit
// validity window so tests can exercise expired material.
func testPair(t *testing.T, domain string, notBefore, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber:          big.NewInt(42),
		Subject:               pkix.Name{CommonName: domain},
		DNSNames:              []string{domain},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM
}

func TestSanitizeDomain(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Example.COM", "example.com"},
		{"*.example.com", "_wildcard_.example.com"},
		{"../../etc/shad", "_.__etc_shad"},
		{"a/b", "a_b"},
		{"", "_"},
		{"panel.test:8443", "panel.test"},
	}

	for _, tc := range cases {
		if got := sanitizeDomain(tc.in); got != tc.want {
			t.Errorf("sanitizeDomain(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSplitPEM(t *testing.T) {
	certPEM, keyPEM := testPair(t, "example.com", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))

	bundle := append(append([]byte{}, keyPEM...), certPEM...)
	gotKey, gotCert := splitPEM(bundle)

	if string(gotKey) != string(keyPEM) {
		t.Error("splitPEM returned the wrong private key block")
	}
	if string(gotCert) != string(certPEM) {
		t.Error("splitPEM returned the wrong certificate block")
	}
}

func TestCertStoreCacheRoundTripKeepsKeysPrivate(t *testing.T) {
	store, err := newCertStore(t.TempDir())
	if err != nil {
		t.Fatalf("newCertStore: %v", err)
	}

	certPEM, keyPEM := testPair(t, "example.com", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	bundle := append(append([]byte{}, keyPEM...), certPEM...)

	ctx := context.Background()
	if err := store.Put(ctx, "example.com", bundle); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := store.Get(ctx, "example.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(bundle) {
		t.Error("cache round trip did not return the stored bundle")
	}

	keyPath := filepath.Join(store.managedDir("example.com"), "privkey.pem")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != keyFileMode {
		t.Errorf("private key mode = %o, want %o", perm, keyFileMode)
	}

	if err := store.Delete(ctx, "example.com"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, "example.com"); !errors.Is(err, autocert.ErrCacheMiss) {
		t.Errorf("Get after Delete error = %v, want ErrCacheMiss", err)
	}
}

func TestCertStoreCacheAccountKeyPathAndMode(t *testing.T) {
	store, err := newCertStore(t.TempDir())
	if err != nil {
		t.Fatalf("newCertStore: %v", err)
	}

	ctx := context.Background()
	if err := store.Put(ctx, "acme_account+key", []byte("account-key-material")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	info, err := os.Stat(filepath.Join(store.acmeRoot(), "acme_account_key"))
	if err != nil {
		t.Fatalf("stat account key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != keyFileMode {
		t.Errorf("account key mode = %o, want %o", perm, keyFileMode)
	}

	got, err := store.Get(ctx, "acme_account+key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "account-key-material" {
		t.Errorf("account key round trip = %q", got)
	}

	if err := store.Delete(ctx, "acme_account+key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, "acme_account+key"); !errors.Is(err, autocert.ErrCacheMiss) {
		t.Errorf("Get after Delete error = %v, want ErrCacheMiss", err)
	}
}

func TestCertStoreIncompleteBundleRejected(t *testing.T) {
	store, err := newCertStore(t.TempDir())
	if err != nil {
		t.Fatalf("newCertStore: %v", err)
	}

	certPEM, _ := testPair(t, "example.com", time.Now(), time.Now().Add(time.Hour))
	if err := store.Put(context.Background(), "example.com", certPEM); err == nil {
		t.Error("Put accepted a bundle with no private key")
	}
}

func TestCertStoreLookupOrderAndExpiry(t *testing.T) {
	dir := t.TempDir()
	store, err := newCertStore(dir)
	if err != nil {
		t.Fatalf("newCertStore: %v", err)
	}
	store.systemDir = filepath.Join(dir, "etc-letsencrypt-live")

	now := time.Now()
	expiredCert, expiredKey := testPair(t, "example.com", now.Add(-48*time.Hour), now.Add(-time.Hour))
	if err := store.Put(context.Background(), "example.com", append(append([]byte{}, expiredKey...), expiredCert...)); err != nil {
		t.Fatalf("Put expired managed cert: %v", err)
	}

	// The only certificate is expired, but serving it beats dropping the site.
	cert, source, ok := store.best("example.com")
	if !ok {
		t.Fatal("best() found nothing, expected the expired managed certificate")
	}
	if source != sourceManaged {
		t.Errorf("source = %v, want sourceManaged", source)
	}
	if !certExpired(cert) {
		t.Error("expected the expired certificate to be reported as expired")
	}

	// A valid operator-supplied certificate under ssl/local wins over it.
	localCert, localKey := testPair(t, "example.com", now.Add(-time.Hour), now.Add(72*time.Hour))
	if err := store.saveLocal("example.com", localCert, localKey); err != nil {
		t.Fatalf("saveLocal: %v", err)
	}

	cert, source, ok = store.best("example.com")
	if !ok || certExpired(cert) {
		t.Fatal("best() should return the valid local certificate")
	}
	if source != sourceLocal {
		t.Errorf("source = %v, want sourceLocal", source)
	}
	if !store.hasLocalCertificate("example.com") {
		t.Error("hasLocalCertificate = false, want true")
	}

	keyInfo, err := os.Stat(filepath.Join(store.localDir("example.com"), "key.pem"))
	if err != nil {
		t.Fatalf("stat local key: %v", err)
	}
	if perm := keyInfo.Mode().Perm(); perm != keyFileMode {
		t.Errorf("local key mode = %o, want %o", perm, keyFileMode)
	}

	// A certbot-managed certificate outranks everything under {data_dir}.
	sysDir := filepath.Join(store.systemDir, "example.com")
	sysCert, sysKey := testPair(t, "example.com", now.Add(-time.Hour), now.Add(240*time.Hour))
	if err := writeFileAtomic(filepath.Join(sysDir, "fullchain.pem"), sysCert, certFileMode); err != nil {
		t.Fatalf("write system cert: %v", err)
	}
	if err := writeFileAtomic(filepath.Join(sysDir, "privkey.pem"), sysKey, keyFileMode); err != nil {
		t.Fatalf("write system key: %v", err)
	}

	_, source, ok = store.best("example.com")
	if !ok || source != sourceSystem {
		t.Errorf("best() source = %v, want sourceSystem", source)
	}
	if !store.hasSystemCertificate("example.com") {
		t.Error("hasSystemCertificate = false, want true")
	}

	// A hostname the certificates do not cover has nothing to serve.
	if _, _, ok := store.best("other.example.org"); ok {
		t.Error("best() matched a certificate for an unrelated hostname")
	}
}

func TestNewCertStoreRequiresDataDir(t *testing.T) {
	if _, err := newCertStore("   "); err == nil {
		t.Error("newCertStore accepted an empty data dir")
	}
}
