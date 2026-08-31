package hosting

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/logging"
	"github.com/webappsgo/cashp/src/security"
)

// challengeLabel is the TXT owner name a tenant publishes to prove control,
// matching the `_verify.{domain}` convention of AI.md PART 36.
const challengeLabel = "_verify"

// challengePath is the HTTP path served from the domain for HTTP proof.
const challengePath = "/.well-known/cashp-challenge/"

// DomainProver performs the external lookups that prove domain control. It is
// an interface so ownership verification is testable without a network.
type DomainProver interface {
	TXTRecords(ctx context.Context, name string) ([]string, error)
	HTTPToken(ctx context.Context, domain, token string) (string, error)
}

// NetProver is the production DomainProver: a DNS TXT lookup and a plain HTTP
// fetch of the challenge file. The HTTP destination is validated against the
// outbound-URL policy so a tenant cannot point verification at an internal
// address.
type NetProver struct {
	// Resolver performs the TXT lookup; nil uses the default resolver.
	Resolver *net.Resolver
	// Client fetches the HTTP challenge; nil uses a 10 second client.
	Client *http.Client
}

// TXTRecords resolves the TXT records of name.
func (p NetProver) TXTRecords(ctx context.Context, name string) ([]string, error) {
	r := p.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return r.LookupTXT(ctx, name)
}

// HTTPToken fetches the challenge file from the domain over plain HTTP.
func (p NetProver) HTTPToken(ctx context.Context, domain, token string) (string, error) {
	url := "http://" + domain + challengePath + token
	if err := security.ValidateOutboundURL(url); err != nil {
		return "", err
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("hosting: challenge fetch returned a non-200 status")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

// ClaimDomain records a tenant's claim over a domain and returns the
// challenge token to publish. A domain already claimed by another tenant is
// refused with a generic message so a probe cannot map domains to tenants.
func (s *Service) ClaimDomain(ctx context.Context, tenantID, rawDomain, method string) (DomainOwnership, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return DomainOwnership{}, err
	}
	domain, err := ValidateDomain(rawDomain)
	if err != nil {
		return DomainOwnership{}, err
	}
	if method != VerifyDNS && method != VerifyHTTP {
		return DomainOwnership{}, invalid("method", "must be dns or http")
	}

	existing, err := s.store.GetOwnership(ctx, domain)
	switch {
	case err == nil && existing.TenantID != tenantID:
		return DomainOwnership{}, apperr.New(apperr.CodeConflict, 409, "that domain is not available")
	case err == nil && existing.Verified:
		return existing, nil
	case err != nil && !apperr.Is(err, apperr.CodeNotFound):
		return DomainOwnership{}, err
	}

	raw, err := security.RandomSecret(security.SecretLen)
	if err != nil {
		return DomainOwnership{}, internalErr(err, "the verification token could not be generated")
	}
	owner := DomainOwnership{
		Domain:    domain,
		TenantID:  tenantID,
		Token:     encodeToken(raw),
		Method:    method,
		CreatedAt: s.now().UTC(),
	}
	if err := s.store.PutOwnership(ctx, owner); err != nil {
		return DomainOwnership{}, err
	}
	s.audit(ctx, "hosting.domain.claim", tenantID, domain, "method", method)
	return owner, nil
}

// ChallengeRecordName returns the TXT owner name a tenant must publish.
func ChallengeRecordName(domain string) string { return challengeLabel + "." + domain }

// VerifyDomain checks the published proof and marks the claim verified.
func (s *Service) VerifyDomain(ctx context.Context, tenantID, rawDomain string) (DomainOwnership, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return DomainOwnership{}, err
	}
	domain, err := ValidateDomain(rawDomain)
	if err != nil {
		return DomainOwnership{}, err
	}
	owner, err := s.store.GetOwnership(ctx, domain)
	if err != nil {
		return DomainOwnership{}, notFound("domain")
	}
	if owner.TenantID != tenantID {
		return DomainOwnership{}, notFound("domain")
	}
	if owner.Verified {
		return owner, nil
	}
	if s.prover == nil {
		return DomainOwnership{}, apperr.New(apperr.CodeUnavailable, 503, "domain verification is unavailable")
	}

	var found bool
	switch owner.Method {
	case VerifyDNS:
		values, lookupErr := s.prover.TXTRecords(ctx, ChallengeRecordName(domain))
		if lookupErr != nil {
			logging.L().Warn("hosting domain verification lookup failed", "error", lookupErr.Error())
			return DomainOwnership{}, apperr.New(apperr.CodeValidation, 422, "the verification record was not found")
		}
		for _, v := range values {
			if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(v)), []byte(owner.Token)) == 1 {
				found = true
				break
			}
		}
	case VerifyHTTP:
		value, fetchErr := s.prover.HTTPToken(ctx, domain, owner.Token)
		if fetchErr != nil {
			logging.L().Warn("hosting domain verification fetch failed", "error", fetchErr.Error())
			return DomainOwnership{}, apperr.New(apperr.CodeValidation, 422, "the verification file was not found")
		}
		found = subtle.ConstantTimeCompare([]byte(value), []byte(owner.Token)) == 1
	default:
		return DomainOwnership{}, invalid("method", "must be dns or http")
	}

	if !found {
		return DomainOwnership{}, apperr.New(apperr.CodeValidation, 422, "the verification value did not match")
	}

	owner.Verified = true
	owner.VerifiedAt = s.now().UTC()
	if err := s.store.PutOwnership(ctx, owner); err != nil {
		return DomainOwnership{}, err
	}
	s.audit(ctx, "hosting.domain.verify", tenantID, domain)
	return owner, nil
}

// ListDomains returns every domain a tenant has claimed.
func (s *Service) ListDomains(ctx context.Context, tenantID string) ([]DomainOwnership, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return nil, err
	}
	return s.store.ListOwnership(ctx, tenantID)
}

// requireOwnedDomain refuses any activation for a domain the tenant has not
// proven it controls. This is the single gate protecting against DNS and mail
// hijack of another tenant's domain.
func (s *Service) requireOwnedDomain(ctx context.Context, tenantID, domain string) error {
	owner, err := s.store.GetOwnership(ctx, domain)
	if err != nil {
		if apperr.Is(err, apperr.CodeNotFound) {
			return apperr.New(apperr.CodeForbidden, 403, "that domain has not been verified for this account")
		}
		return err
	}
	if owner.TenantID != tenantID || !owner.Verified {
		return apperr.New(apperr.CodeForbidden, 403, "that domain has not been verified for this account")
	}
	return nil
}

// encodeToken renders a random secret as a DNS- and URL-safe token.
func encodeToken(raw []byte) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, len(raw))
	for i, b := range raw {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out)
}

// audit writes one entry to the append-only audit log. Every value passed
// here is a validated identifier or domain, never a secret.
func (s *Service) audit(ctx context.Context, event, tenantID, target string, extra ...any) {
	attrs := make([]any, 0, 6+len(extra))
	attrs = append(attrs, "event", event, "tenant", tenantID, "target", target)
	attrs = append(attrs, extra...)
	logging.Audit().InfoContext(ctx, "hosting", attrs...)
}
