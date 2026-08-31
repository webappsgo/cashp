package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// maxVerificationChecks bounds how many times a single pending domain is polled before
// it is parked in the failed state, so a permanently misconfigured zone cannot keep the
// scheduler querying DNS forever.
const maxVerificationChecks = 96

// DomainOwner identifies the tenant a custom domain belongs to.
type DomainOwner struct {
	Type string
	ID   int64
}

// ownerValid reports whether the owner tuple is one this package will act on.
func (o DomainOwner) ownerValid() bool {
	return (o.Type == OwnerUser || o.Type == OwnerOrg) && o.ID > 0
}

// AddDomain registers a custom domain in the pending state. Nothing is served and no
// certificate is requested until ownership has been proven with the DNS TXT record.
func (s *Service) AddDomain(ctx context.Context, owner DomainOwner, actorID int64, domain string) (*CustomDomain, *apperr.Error) {
	if !s.cfg.DomainsEnabled {
		return nil, ErrFeatureDisabled("Custom domains")
	}
	if !owner.ownerValid() {
		return nil, ErrForbidden()
	}
	normalized, isApex, isWildcard, err := ValidateDomain(domain, s.cfg.AllowWildcards, s.cfg.ReservedDomains)
	if err != nil {
		return nil, ErrDomainInvalid(err.Error())
	}

	if s.cfg.MaxDomainsPerOwner > 0 {
		count, err := s.store.CountDomains(ctx, owner.Type, owner.ID)
		if err != nil {
			return nil, ErrInternal(err)
		}
		if count >= s.cfg.MaxDomainsPerOwner {
			return nil, ErrQuota("You have reached the maximum number of custom domains")
		}
	}
	// A domain is globally unique. The conflict is reported without naming the current
	// holder, so this endpoint cannot be used to enumerate other tenants' domains.
	if _, err := s.store.DomainByName(ctx, normalized); err == nil {
		return nil, ErrDomainTaken()
	}

	token, err := NewVerificationToken()
	if err != nil {
		return nil, ErrInternal(err)
	}
	d := &CustomDomain{
		OwnerType:          owner.Type,
		OwnerID:            owner.ID,
		Domain:             normalized,
		IsApex:             isApex,
		IsWildcard:         isWildcard,
		VerificationStatus: VerificationPending,
		VerificationToken:  token,
		SSLEnabled:         SSLEligible(normalized),
		SSLStatus:          SSLStatusNone,
		Status:             DomainStatusPending,
	}
	if _, err := s.store.CreateDomain(ctx, d); err != nil {
		return nil, ErrInternal(err)
	}
	if err := s.store.RecordDomainAudit(ctx, d.ID, "domain.added", OwnerUser, actorID, normalized); err != nil {
		s.log.Warn("record domain audit", slog.String("error", err.Error()))
	}
	s.audit("domain.added",
		slog.Int64("domain_id", d.ID),
		slog.String("owner_type", owner.Type),
		slog.Int64("owner_id", owner.ID),
		slog.Int64("actor_id", actorID),
		slog.String("domain", normalized))
	return d, nil
}

// ListDomains returns one tenant's domains. Every row is fetched with the owner in the
// WHERE clause, so no caller can read another tenant's list by changing an identifier.
func (s *Service) ListDomains(ctx context.Context, owner DomainOwner) ([]*CustomDomain, *apperr.Error) {
	if !owner.ownerValid() {
		return nil, ErrForbidden()
	}
	rows, err := s.store.ListDomains(ctx, owner.Type, owner.ID)
	if err != nil {
		return nil, ErrInternal(err)
	}
	return rows, nil
}

// GetDomain fetches one of the tenant's own domains. A domain owned by somebody else is
// reported as missing rather than forbidden.
func (s *Service) GetDomain(ctx context.Context, owner DomainOwner, domain string) (*CustomDomain, *apperr.Error) {
	if !owner.ownerValid() {
		return nil, ErrForbidden()
	}
	d, err := s.store.DomainByNameForOwner(ctx, owner.Type, owner.ID, NormalizeDomain(domain))
	if err != nil {
		return nil, ErrNotFound("Domain")
	}
	return d, nil
}

// VerificationInstructions returns the exact DNS record the owner must publish.
func (s *Service) VerificationInstructions(d *CustomDomain) (name, value string) {
	return VerificationRecordName(s.cfg.DomainVerificationPrefix, d.Domain), d.VerificationToken
}

// VerifyDomain performs the ownership check on demand. It is rate limited by the caller
// and additionally capped by the per-domain check counter.
func (s *Service) VerifyDomain(ctx context.Context, owner DomainOwner, actorID int64, domain string) (*CustomDomain, *apperr.Error) {
	d, aerr := s.GetDomain(ctx, owner, domain)
	if aerr != nil {
		return nil, aerr
	}
	if d.VerificationStatus == VerificationVerified {
		return d, nil
	}
	if err := s.runVerification(ctx, d); err != nil {
		return nil, ErrDomainVerificationFailed()
	}
	if err := s.store.RecordDomainAudit(ctx, d.ID, "domain.verified", OwnerUser, actorID, d.Domain); err != nil {
		s.log.Warn("record domain audit", slog.String("error", err.Error()))
	}
	s.audit("domain.verified",
		slog.Int64("domain_id", d.ID),
		slog.Int64("actor_id", actorID),
		slog.String("domain", d.Domain))
	return d, nil
}

// runVerification drives the state machine for one domain: pending -> verified moves the
// domain to active (or leaves it pending operator approval), and a failed lookup only
// records the attempt. The row is never advanced on anything but a positive TXT match.
func (s *Service) runVerification(ctx context.Context, d *CustomDomain) error {
	err := VerifyDomainOwnership(ctx, s.resolver, s.cfg.DomainVerificationPrefix, d.Domain, d.VerificationToken)
	if err != nil {
		status := VerificationPending
		if d.CheckCount+1 >= maxVerificationChecks {
			status = VerificationFailed
		}
		if markErr := s.store.MarkDomainChecked(ctx, d.ID, status); markErr != nil {
			s.log.Warn("mark domain checked", slog.String("error", markErr.Error()))
		}
		d.CheckCount++
		d.VerificationStatus = status
		return err
	}

	status := DomainStatusActive
	if s.cfg.DomainsRequireApproval {
		status = DomainStatusPending
	}
	if err := s.store.MarkDomainVerified(ctx, d.ID, status); err != nil {
		return err
	}
	d.VerificationStatus = VerificationVerified
	d.VerifiedAt = time.Now().Unix()
	d.Status = status

	if status == DomainStatusActive {
		s.requestCertificate(ctx, d)
	}
	return nil
}

// requestCertificate asks the TLS manager for a certificate. Failure is recorded on the
// domain and retried by the renewal task; it never blocks activation, because a domain
// can still be served over plain HTTP while issuance is pending.
func (s *Service) requestCertificate(ctx context.Context, d *CustomDomain) {
	if !SSLEligible(d.Domain) {
		// Overlay hosts get neither an ACME certificate nor HSTS: no public CA can
		// validate them and the header could never be satisfied.
		if err := s.store.DisableDomainSSL(ctx, d.ID); err != nil {
			s.log.Warn("disable domain ssl", slog.String("error", err.Error()))
		}
		return
	}
	if s.certs == nil {
		return
	}
	challenge := SelectChallenge(d.IsWildcard, d.SSLProvider, false)
	if err := s.store.SetDomainSSLPending(ctx, d.ID, challenge); err != nil {
		s.log.Warn("set domain ssl pending", slog.String("error", err.Error()))
		return
	}
	if err := s.certs.AddDomain(ctx, d.Domain); err != nil {
		// The operator-facing reason is stored, but only a generic failure is ever
		// returned to the tenant so issuer internals stay inside the server.
		if setErr := s.store.SetDomainSSLError(ctx, d.ID, "certificate issuance failed"); setErr != nil {
			s.log.Warn("set domain ssl error", slog.String("error", setErr.Error()))
		}
		s.log.Warn("issue certificate",
			slog.String("domain", d.Domain),
			slog.String("error", err.Error()))
		return
	}
	s.audit("domain.certificate_requested",
		slog.Int64("domain_id", d.ID),
		slog.String("domain", d.Domain),
		slog.String("challenge", challenge))
}

// ActivateDomain is the Server Admin approval step used when DomainsRequireApproval is
// set. A domain that has not proven ownership can never be activated.
func (s *Service) ActivateDomain(ctx context.Context, adminID, domainID int64) *apperr.Error {
	d, err := s.store.DomainByID(ctx, domainID)
	if err != nil {
		return ErrNotFound("Domain")
	}
	if d.VerificationStatus != VerificationVerified {
		return ErrDomainNotVerified()
	}
	if err := s.store.SetDomainStatus(ctx, d.ID, DomainStatusActive, ""); err != nil {
		return ErrInternal(err)
	}
	d.Status = DomainStatusActive
	s.requestCertificate(ctx, d)
	if err := s.store.RecordDomainAudit(ctx, d.ID, "domain.activated", OwnerAdmin, adminID, d.Domain); err != nil {
		s.log.Warn("record domain audit", slog.String("error", err.Error()))
	}
	s.audit("domain.activated",
		slog.Int64("domain_id", d.ID),
		slog.Int64("admin_id", adminID),
		slog.String("domain", d.Domain))
	return nil
}

// SuspendDomain parks a domain, used by a Server Admin on abuse.
func (s *Service) SuspendDomain(ctx context.Context, adminID, domainID int64, reason string) *apperr.Error {
	d, err := s.store.DomainByID(ctx, domainID)
	if err != nil {
		return ErrNotFound("Domain")
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > 500 {
		return ErrValidation("reason", "Keep the reason under 500 characters")
	}
	if err := s.store.SetDomainStatus(ctx, d.ID, DomainStatusSuspended, reason); err != nil {
		return ErrInternal(err)
	}
	if s.certs != nil {
		if err := s.certs.RemoveDomain(d.Domain); err != nil {
			s.log.Warn("remove certificate",
				slog.String("domain", d.Domain),
				slog.String("error", err.Error()))
		}
	}
	if err := s.store.RecordDomainAudit(ctx, d.ID, "domain.suspended", OwnerAdmin, adminID, reason); err != nil {
		s.log.Warn("record domain audit", slog.String("error", err.Error()))
	}
	s.audit("domain.suspended",
		slog.Int64("domain_id", d.ID),
		slog.Int64("admin_id", adminID),
		slog.String("domain", d.Domain))
	return nil
}

// DeleteDomain removes a tenant's domain and retires its certificate. The DELETE is
// scoped to the owner, so an identifier from another tenant matches no row.
func (s *Service) DeleteDomain(ctx context.Context, owner DomainOwner, actorID int64, domain string) *apperr.Error {
	d, aerr := s.GetDomain(ctx, owner, domain)
	if aerr != nil {
		return aerr
	}
	if err := s.store.DeleteDomain(ctx, owner.Type, owner.ID, d.ID); err != nil {
		return ErrInternal(err)
	}
	if s.certs != nil {
		if err := s.certs.RemoveDomain(d.Domain); err != nil {
			s.log.Warn("remove certificate",
				slog.String("domain", d.Domain),
				slog.String("error", err.Error()))
		}
	}
	s.audit("domain.deleted",
		slog.Int64("domain_id", d.ID),
		slog.String("owner_type", owner.Type),
		slog.Int64("owner_id", owner.ID),
		slog.Int64("actor_id", actorID),
		slog.String("domain", d.Domain))
	return nil
}

// RunDomainVerification re-checks every pending domain. Bound to the scheduler.
func (s *Service) RunDomainVerification(ctx context.Context) error {
	if !s.cfg.DomainsEnabled {
		return nil
	}
	rows, err := s.store.ListDomainsForVerification(ctx, time.Now().Add(-15*time.Minute).Unix(), 100)
	if err != nil {
		return err
	}
	for _, d := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.runVerification(ctx, d); err != nil && !errors.Is(err, ErrDomainVerifyMiss) {
			s.log.Warn("verify domain",
				slog.String("domain", d.Domain),
				slog.String("error", err.Error()))
		}
	}
	return nil
}

// RunDomainSSLRenewal re-requests certificates that are close to expiry or errored.
func (s *Service) RunDomainSSLRenewal(ctx context.Context) error {
	if !s.cfg.DomainsEnabled || s.certs == nil {
		return nil
	}
	rows, err := s.store.ListDomainsForRenewal(ctx, time.Now().Add(30*24*time.Hour).Unix(), 100)
	if err != nil {
		return err
	}
	for _, d := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.requestCertificate(ctx, d)
	}
	return nil
}

// RunDomainCleanup drops domains that never completed verification within the window.
func (s *Service) RunDomainCleanup(ctx context.Context) error {
	if !s.cfg.DomainsEnabled {
		return nil
	}
	cutoff := time.Now().Add(-s.cfg.DomainVerificationTTL).Unix()
	removed, err := s.store.PurgeStaleDomains(ctx, cutoff)
	if err != nil {
		return err
	}
	if removed > 0 {
		s.audit("domain.cleanup", slog.Int64("removed", removed))
	}
	return nil
}
