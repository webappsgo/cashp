package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/webappsgo/cashp/src/logging"
	"github.com/webappsgo/cashp/src/security"
)

// Renderer is the subset of src/web the auth handlers use. Declaring it here as an
// interface keeps this package compilable and testable on its own.
type Renderer interface {
	Render(w http.ResponseWriter, r *http.Request, name string, data any) error
	RenderStatus(w http.ResponseWriter, r *http.Request, status int, name string, data any) error
	RenderError(w http.ResponseWriter, r *http.Request, status int, code, message string)
}

// Mailer sends the transactional messages this package produces. src/notify supplies
// the real implementation; a nil Mailer makes every send a no-op, which keeps first
// run working with zero configuration.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// CertIssuer is the subset of src/tlsmgr used to obtain custom domain certificates.
type CertIssuer interface {
	AddDomain(ctx context.Context, domain string) error
	RemoveDomain(domain string) error
}

// Options are the collaborators src/server injects when constructing the service.
type Options struct {
	Store    *Store
	Config   Config
	Limits   *security.Limits
	Renderer Renderer
	Mailer   Mailer
	Certs    CertIssuer
	Resolver DNSResolver
	// CSRFSecret must be at least security.SecretLen bytes and stable across restarts,
	// otherwise every issued form token is invalidated on reload.
	CSRFSecret []byte
}

// Service holds the auth, organization and custom-domain business logic. Handlers are
// thin wrappers over its methods so the rules are testable without an HTTP server.
type Service struct {
	store    *Store
	cfg      Config
	limits   *security.Limits
	renderer Renderer
	mailer   Mailer
	certs    CertIssuer
	resolver DNSResolver
	csrfKey  []byte
	log      *slog.Logger
	// pages holds this package's own server-rendered templates, parsed once at start-up
	// so a malformed page fails the build rather than a request.
	pages *pageSet
	// dummyHash is a real Argon2id hash of a random value. Login verifies against it
	// when no account matched, so a missing account costs the same wall-clock time as
	// a wrong password and cannot be distinguished by timing.
	dummyHash string
	// minAuthTime is the floor every credential check is padded up to, which absorbs
	// the residual difference between the lookup paths.
	minAuthTime time.Duration
}

// New builds the service. It fails only when the environment cannot produce the
// entropy the dummy hash and CSRF key need.
func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("auth: store is required")
	}
	cfg := opts.Config
	cfg.normalize()
	AddBlocklistEntries(cfg.ExtraReservedNames)

	filler := make([]byte, 32)
	if _, err := rand.Read(filler); err != nil {
		return nil, fmt.Errorf("auth: generate dummy secret: %w", err)
	}
	dummy, err := security.HashPassword(hex.EncodeToString(filler))
	if err != nil {
		return nil, fmt.Errorf("auth: build dummy hash: %w", err)
	}

	key := opts.CSRFSecret
	if len(key) < security.SecretLen {
		key, err = security.RandomSecret(security.SecretLen)
		if err != nil {
			return nil, fmt.Errorf("auth: generate csrf secret: %w", err)
		}
	}

	limits := opts.Limits
	if limits == nil {
		limits = security.NewLimits()
	}
	resolver := opts.Resolver
	if resolver == nil {
		resolver = defaultResolver{}
	}

	pages, err := newPageSet()
	if err != nil {
		return nil, err
	}

	return &Service{
		store:       opts.Store,
		cfg:         cfg,
		limits:      limits,
		renderer:    opts.Renderer,
		mailer:      opts.Mailer,
		certs:       opts.Certs,
		resolver:    resolver,
		csrfKey:     key,
		log:         logging.L(),
		pages:       pages,
		dummyHash:   dummy,
		minAuthTime: 250 * time.Millisecond,
	}, nil
}

// Config exposes the effective configuration, mainly so handlers can branch on the
// feature switches without holding a second copy.
func (s *Service) Config() Config { return s.cfg }

// Store exposes the persistence layer for the admin panel and the scheduler tasks.
func (s *Service) Store() *Store { return s.store }

// audit writes one line to the dedicated audit logger. Every destructive action and
// every authentication decision goes through here. Values are already sanitized by the
// caller; secrets are never passed in.
func (s *Service) audit(action string, attrs ...slog.Attr) {
	logging.Audit().LogAttrs(context.Background(), slog.LevelInfo, action, attrs...)
}

// newSecret returns a random 32-byte hex string used for session cookies, reset links,
// verification links and invite codes.
func newSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// pad blocks until at least minAuthTime has elapsed since start. Applied to every
// credential path so that "no such account", "wrong password" and "locked out" are
// indistinguishable by response time as well as by response body.
func (s *Service) pad(start time.Time) {
	if elapsed := time.Since(start); elapsed < s.minAuthTime {
		time.Sleep(s.minAuthTime - elapsed)
	}
}

// checkPassword verifies a candidate against a stored hash and reports whether the
// stored hash needs upgrading. An empty stored hash still runs a full verification
// against the dummy hash so the work is identical either way.
func (s *Service) checkPassword(stored, candidate string) (ok bool, needsRehash bool) {
	if stored == "" {
		stored = s.dummyHash
	}
	ok, needsRehash, err := security.VerifyPassword(stored, candidate)
	if err != nil {
		return false, false
	}
	return ok, needsRehash
}
