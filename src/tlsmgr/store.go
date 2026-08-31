package tlsmgr

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

const (
	// keyFileMode is the permission every private key on disk must carry.
	keyFileMode = os.FileMode(0o600)

	// certFileMode is the permission for public certificate material.
	certFileMode = os.FileMode(0o644)

	// dirMode is the permission for every directory the store creates.
	dirMode = os.FileMode(0o700)
)

// defaultSystemCertDir is the certbot-managed directory cashp reads from but
// never renews, per AI.md PART 15 "Certificate Management Ownership".
const defaultSystemCertDir = "/etc/letsencrypt/live"

// certSource identifies where a certificate came from, which decides whether
// cashp is allowed to renew it.
type certSource int

const (
	// sourceSystem is /etc/letsencrypt/live/** — certbot owns renewal.
	sourceSystem certSource = iota

	// sourceManaged is {data_dir}/ssl/letsencrypt/{fqdn}/ — cashp renews it.
	sourceManaged

	// sourceLocal is {data_dir}/ssl/local/{fqdn}/ — operator-supplied static
	// certificates and the self-signed fallback; never auto-renewed.
	sourceLocal
)

// candidate is one certificate found on disk together with the provenance
// and validity facts the manager needs to choose between candidates.
type candidate struct {
	cert    *tls.Certificate
	source  certSource
	path    string
	expired bool
	matches bool
}

// certStore is the on-disk certificate store. It doubles as the
// autocert.Cache implementation so ACME material lands in the directory
// layout AI.md PART 15 specifies instead of an opaque blob directory.
type certStore struct {
	dataDir   string
	systemDir string
}

// newCertStore builds a store rooted at dataDir, creating the ssl tree.
func newCertStore(dataDir string) (*certStore, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("tlsmgr: data dir is required")
	}

	s := &certStore{dataDir: dataDir, systemDir: defaultSystemCertDir}
	for _, dir := range []string{s.managedRoot(), s.localRoot(), s.acmeRoot()} {
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// sslRoot is {data_dir}/ssl.
func (s *certStore) sslRoot() string { return filepath.Join(s.dataDir, "ssl") }

// managedRoot is {data_dir}/ssl/letsencrypt.
func (s *certStore) managedRoot() string { return filepath.Join(s.sslRoot(), "letsencrypt") }

// localRoot is {data_dir}/ssl/local.
func (s *certStore) localRoot() string { return filepath.Join(s.sslRoot(), "local") }

// acmeRoot holds ACME account keys and in-flight challenge tokens.
func (s *certStore) acmeRoot() string { return filepath.Join(s.sslRoot(), "acme") }

// managedDir is the app-renewed directory for one domain.
func (s *certStore) managedDir(domain string) string {
	return filepath.Join(s.managedRoot(), sanitizeDomain(domain))
}

// localDir is the operator-owned directory for one domain.
func (s *certStore) localDir(domain string) string {
	return filepath.Join(s.localRoot(), sanitizeDomain(domain))
}

// sanitizeDomain maps a hostname to a single safe path element. Wildcards
// become a literal prefix and any separator or traversal input is rejected
// into a flat, harmless name.
func sanitizeDomain(domain string) string {
	d := NormalizeHost(domain)
	d = strings.ReplaceAll(d, "*", "_wildcard_")

	var b strings.Builder
	for _, r := range d {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}

	out := strings.TrimLeft(b.String(), ".")
	out = strings.ReplaceAll(out, "..", "._")
	if out == "" {
		return "_"
	}

	return out
}

// writeFileAtomic writes data with mode via a temp file in the same
// directory, so a crash mid-write never leaves a truncated key or chain.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}

	return nil
}

// splitPEM separates a PEM bundle into its private key blocks and its
// certificate blocks, preserving order within each group.
func splitPEM(data []byte) (keyPEM, certPEM []byte) {
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}

		encoded := pem.EncodeToMemory(block)
		if strings.Contains(block.Type, "PRIVATE KEY") {
			keyPEM = append(keyPEM, encoded...)
			continue
		}

		if block.Type == "CERTIFICATE" {
			certPEM = append(certPEM, encoded...)
		}
	}

	return keyPEM, certPEM
}

// isDomainCacheKey reports whether an autocert cache key names a certificate
// bundle for a host, as opposed to an account key or a challenge token.
func isDomainCacheKey(key string) bool {
	if key == "" || strings.Contains(key, "+") {
		return false
	}

	if strings.ContainsAny(key, `/\`) || strings.Contains(key, "..") {
		return false
	}

	return strings.Contains(key, ".")
}

// Get implements autocert.Cache. Domain bundles are reassembled from the
// split key/chain files; everything else comes from the acme directory.
func (s *certStore) Get(_ context.Context, key string) ([]byte, error) {
	if isDomainCacheKey(key) {
		dir := s.managedDir(key)
		keyPEM, err := os.ReadFile(filepath.Join(dir, "privkey.pem"))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, autocert.ErrCacheMiss
			}
			return nil, err
		}

		certPEM, err := os.ReadFile(filepath.Join(dir, "fullchain.pem"))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, autocert.ErrCacheMiss
			}
			return nil, err
		}

		return append(append([]byte{}, keyPEM...), certPEM...), nil
	}

	data, err := os.ReadFile(filepath.Join(s.acmeRoot(), sanitizeDomain(key)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, autocert.ErrCacheMiss
		}
		return nil, err
	}

	return data, nil
}

// Put implements autocert.Cache, writing private material with 0600.
func (s *certStore) Put(_ context.Context, key string, data []byte) error {
	if !isDomainCacheKey(key) {
		return writeFileAtomic(filepath.Join(s.acmeRoot(), sanitizeDomain(key)), data, keyFileMode)
	}

	keyPEM, certPEM := splitPEM(data)
	if len(keyPEM) == 0 || len(certPEM) == 0 {
		return errors.New("tlsmgr: refusing to store an incomplete certificate bundle")
	}

	dir := s.managedDir(key)
	if err := writeFileAtomic(filepath.Join(dir, "privkey.pem"), keyPEM, keyFileMode); err != nil {
		return err
	}

	return writeFileAtomic(filepath.Join(dir, "fullchain.pem"), certPEM, certFileMode)
}

// Delete implements autocert.Cache.
func (s *certStore) Delete(_ context.Context, key string) error {
	if !isDomainCacheKey(key) {
		if err := os.Remove(filepath.Join(s.acmeRoot(), sanitizeDomain(key))); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	if err := os.RemoveAll(s.managedDir(key)); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// loadPair reads a certificate/key pair and returns a candidate describing
// how usable it is for domain. A missing pair yields ok=false.
func loadPair(certPath, keyPath, domain string, source certSource) (candidate, bool) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return candidate{}, false
	}

	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return candidate{}, false
	}

	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return candidate{}, false
	}

	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return candidate{}, false
	}
	pair.Leaf = leaf

	now := time.Now()
	return candidate{
		cert:    &pair,
		source:  source,
		path:    certPath,
		expired: now.After(leaf.NotAfter) || now.Before(leaf.NotBefore),
		matches: leaf.VerifyHostname(domain) == nil,
	}, true
}

// candidates returns every certificate on disk for domain, in the lookup
// order AI.md PART 15 mandates: system certbot dirs first, then the
// app-managed Let's Encrypt dir, then the operator's local dir.
func (s *certStore) candidates(domain string) []candidate {
	d := NormalizeHost(domain)
	if d == "" {
		return nil
	}

	type location struct {
		certPath string
		keyPath  string
		source   certSource
	}

	locations := []location{
		{filepath.Join(s.systemDir, "domain", "fullchain.pem"), filepath.Join(s.systemDir, "domain", "privkey.pem"), sourceSystem},
		{filepath.Join(s.systemDir, sanitizeDomain(d), "fullchain.pem"), filepath.Join(s.systemDir, sanitizeDomain(d), "privkey.pem"), sourceSystem},
		{filepath.Join(s.managedDir(d), "fullchain.pem"), filepath.Join(s.managedDir(d), "privkey.pem"), sourceManaged},
		{filepath.Join(s.localDir(d), "cert.pem"), filepath.Join(s.localDir(d), "key.pem"), sourceLocal},
	}

	out := make([]candidate, 0, len(locations))
	for _, loc := range locations {
		if c, ok := loadPair(loc.certPath, loc.keyPath, d, loc.source); ok {
			out = append(out, c)
		}
	}

	return out
}

// best picks the certificate to serve for domain: the first non-expired
// hostname match, otherwise the first hostname match that has expired, so a
// failed renewal keeps the site on its existing certificate rather than
// taking it down.
func (s *certStore) best(domain string) (*tls.Certificate, certSource, bool) {
	found := s.candidates(domain)

	for _, c := range found {
		if c.matches && !c.expired {
			return c.cert, c.source, true
		}
	}

	for _, c := range found {
		if c.matches {
			return c.cert, c.source, true
		}
	}

	return nil, sourceLocal, false
}

// managedCertificate returns the app-renewed certificate for domain, if any.
func (s *certStore) managedCertificate(domain string) (*tls.Certificate, bool) {
	dir := s.managedDir(domain)
	c, ok := loadPair(filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem"), domain, sourceManaged)
	if !ok {
		return nil, false
	}

	return c.cert, true
}

// hasSystemCertificate reports whether certbot already owns a matching,
// unexpired certificate for domain, in which case cashp must not renew it.
func (s *certStore) hasSystemCertificate(domain string) bool {
	for _, c := range s.candidates(domain) {
		if c.source == sourceSystem && c.matches && !c.expired {
			return true
		}
	}

	return false
}

// hasLocalCertificate reports whether the operator supplied a static
// certificate for domain, which cashp serves but never auto-renews.
func (s *certStore) hasLocalCertificate(domain string) bool {
	for _, c := range s.candidates(domain) {
		if c.source == sourceLocal && c.matches && !c.expired {
			return true
		}
	}

	return false
}

// saveLocal writes an operator-style pair (used by the self-signed fallback)
// into {data_dir}/ssl/local/{fqdn}/ with a 0600 private key.
func (s *certStore) saveLocal(domain string, certPEM, keyPEM []byte) error {
	dir := s.localDir(domain)
	if err := writeFileAtomic(filepath.Join(dir, "key.pem"), keyPEM, keyFileMode); err != nil {
		return err
	}

	return writeFileAtomic(filepath.Join(dir, "cert.pem"), certPEM, certFileMode)
}
