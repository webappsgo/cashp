package auth

import (
	"context"
	"testing"
	"time"
)

// newTestDomain creates a pending custom domain and fails the test if the
// returned id is zero — the regression this whole pass exists to catch.
func newTestDomain(t *testing.T, store *Store, ownerType string, ownerID int64, domain string) *CustomDomain {
	t.Helper()
	d := &CustomDomain{
		OwnerType:          ownerType,
		OwnerID:            ownerID,
		Domain:             domain,
		IsApex:             true,
		VerificationStatus: VerificationPending,
		VerificationToken:  "cashp-verify=faketoken",
		Status:             DomainStatusPending,
	}
	id, err := store.CreateDomain(context.Background(), d)
	if err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	if id == 0 {
		t.Fatal("CreateDomain returned id 0")
	}
	if d.ID == 0 {
		t.Fatal("CreateDomain left CustomDomain.ID at 0")
	}
	return d
}

func TestCreateDomainAndLookups(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "domainowner", "domainowner@example.com")
	// CreateDomain stores the domain exactly as given — normalization is the
	// Service layer's responsibility (via ValidateDomain before insert) — so
	// the row is seeded already-normalized here, matching every real caller.
	d := newTestDomain(t, store, OwnerUser, owner.ID, "example.com")

	// DomainByName still normalizes its *query* argument, so a differently-cased
	// lookup against an already-normalized row must still resolve.
	byName, err := store.DomainByName(ctx, "EXAMPLE.com")
	if err != nil {
		t.Fatalf("DomainByName (normalized): %v", err)
	}
	if byName.ID != d.ID {
		t.Error("DomainByName resolved a different domain")
	}
	if byName.Domain != "example.com" {
		t.Errorf("stored domain = %q, want example.com", byName.Domain)
	}

	byID, err := store.DomainByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("DomainByID: %v", err)
	}
	if byID.OwnerID != owner.ID {
		t.Errorf("DomainByID OwnerID = %d, want %d", byID.OwnerID, owner.ID)
	}
}

func TestDomainByNameNotFound(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	if _, err := store.DomainByName(context.Background(), "nowhere.example"); err == nil {
		t.Error("DomainByName(missing) = nil error, want not-found error")
	}
}

func TestDomainByIDForOwnerScoped(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "scopeddomainowner", "scopeddomainowner@example.com")
	other := newTestUser(t, store, "scopeddomainother", "scopeddomainother@example.com")
	d := newTestDomain(t, store, OwnerUser, owner.ID, "scoped-domain.example")

	found, err := store.DomainByIDForOwner(ctx, OwnerUser, owner.ID, d.ID)
	if err != nil {
		t.Fatalf("DomainByIDForOwner(correct owner): %v", err)
	}
	if found.ID != d.ID {
		t.Error("DomainByIDForOwner resolved a different domain")
	}

	if _, err := store.DomainByIDForOwner(ctx, OwnerUser, other.ID, d.ID); err == nil {
		t.Error("DomainByIDForOwner(wrong owner) succeeded, want not-found — cross-tenant leak")
	}
}

func TestDomainByNameForOwnerScoped(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "namescopedowner", "namescopedowner@example.com")
	other := newTestUser(t, store, "namescopedother", "namescopedother@example.com")
	newTestDomain(t, store, OwnerUser, owner.ID, "name-scoped.example")

	if _, err := store.DomainByNameForOwner(ctx, OwnerUser, owner.ID, "name-scoped.example"); err != nil {
		t.Errorf("DomainByNameForOwner(correct owner): %v", err)
	}
	if _, err := store.DomainByNameForOwner(ctx, OwnerUser, other.ID, "name-scoped.example"); err == nil {
		t.Error("DomainByNameForOwner(wrong owner) succeeded, want not-found — cross-tenant leak")
	}
}

func TestListDomains(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "listdomainsowner", "listdomainsowner@example.com")
	newTestDomain(t, store, OwnerUser, owner.ID, "list-a.example")
	newTestDomain(t, store, OwnerUser, owner.ID, "list-b.example")

	list, err := store.ListDomains(ctx, OwnerUser, owner.ID)
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("ListDomains len = %d, want 2", len(list))
	}
}

func TestListDomainsEmpty(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "nodomainsowner", "nodomainsowner@example.com")
	list, err := store.ListDomains(ctx, OwnerUser, owner.ID)
	if err != nil {
		t.Fatalf("ListDomains(empty): %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListDomains(empty) len = %d, want 0", len(list))
	}
}

func TestCountDomains(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "countdomainsowner", "countdomainsowner@example.com")
	newTestDomain(t, store, OwnerUser, owner.ID, "count-a.example")

	n, err := store.CountDomains(ctx, OwnerUser, owner.ID)
	if err != nil {
		t.Fatalf("CountDomains: %v", err)
	}
	if n != 1 {
		t.Errorf("CountDomains = %d, want 1", n)
	}
}

func TestListDomainsForVerification(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "verifdomainsowner", "verifdomainsowner@example.com")
	d := newTestDomain(t, store, OwnerUser, owner.ID, "due-for-check.example")

	// last_check_at starts at 0, so any future cutoff should surface it.
	due, err := store.ListDomainsForVerification(ctx, time.Now().Add(time.Hour).Unix(), 0)
	if err != nil {
		t.Fatalf("ListDomainsForVerification: %v", err)
	}
	found := false
	for _, dd := range due {
		if dd.ID == d.ID {
			found = true
		}
	}
	if !found {
		t.Error("ListDomainsForVerification did not surface a pending, never-checked domain")
	}
}

func TestListActiveDomains(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "activedomainsowner", "activedomainsowner@example.com")
	d := newTestDomain(t, store, OwnerUser, owner.ID, "active-domain.example")

	// Not verified/active yet — must not appear.
	active, err := store.ListActiveDomains(ctx)
	if err != nil {
		t.Fatalf("ListActiveDomains (before): %v", err)
	}
	for _, dd := range active {
		if dd.ID == d.ID {
			t.Fatal("pending domain appeared in ListActiveDomains before verification")
		}
	}

	if err := store.MarkDomainVerified(ctx, d.ID, DomainStatusActive); err != nil {
		t.Fatalf("MarkDomainVerified: %v", err)
	}

	active, err = store.ListActiveDomains(ctx)
	if err != nil {
		t.Fatalf("ListActiveDomains (after): %v", err)
	}
	found := false
	for _, dd := range active {
		if dd.ID == d.ID {
			found = true
		}
	}
	if !found {
		t.Error("ListActiveDomains did not surface a verified, active domain")
	}
}

func TestMarkDomainCheckedIncrementsCount(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "checkdomainsowner", "checkdomainsowner@example.com")
	d := newTestDomain(t, store, OwnerUser, owner.ID, "checked-domain.example")

	if err := store.MarkDomainChecked(ctx, d.ID, VerificationFailed); err != nil {
		t.Fatalf("MarkDomainChecked: %v", err)
	}
	reloaded, err := store.DomainByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("DomainByID: %v", err)
	}
	if reloaded.VerificationStatus != VerificationFailed {
		t.Errorf("VerificationStatus = %q, want %q", reloaded.VerificationStatus, VerificationFailed)
	}
	if reloaded.CheckCount != 1 {
		t.Errorf("CheckCount = %d, want 1", reloaded.CheckCount)
	}
	if reloaded.LastCheckAt == 0 {
		t.Error("MarkDomainChecked did not set LastCheckAt")
	}
}

func TestMarkDomainVerified(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "verifieddomainsowner", "verifieddomainsowner@example.com")
	d := newTestDomain(t, store, OwnerUser, owner.ID, "verified-domain.example")

	if err := store.MarkDomainVerified(ctx, d.ID, DomainStatusActive); err != nil {
		t.Fatalf("MarkDomainVerified: %v", err)
	}
	reloaded, err := store.DomainByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("DomainByID: %v", err)
	}
	if reloaded.VerificationStatus != VerificationVerified {
		t.Errorf("VerificationStatus = %q, want %q", reloaded.VerificationStatus, VerificationVerified)
	}
	if reloaded.Status != DomainStatusActive {
		t.Errorf("Status = %q, want %q", reloaded.Status, DomainStatusActive)
	}
	if reloaded.VerifiedAt == 0 {
		t.Error("MarkDomainVerified did not set VerifiedAt")
	}
}

func TestSetDomainStatus(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "statusdomainsowner", "statusdomainsowner@example.com")
	d := newTestDomain(t, store, OwnerUser, owner.ID, "status-domain.example")

	if err := store.SetDomainStatus(ctx, d.ID, DomainStatusSuspended, "abuse report"); err != nil {
		t.Fatalf("SetDomainStatus: %v", err)
	}
	reloaded, err := store.DomainByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("DomainByID: %v", err)
	}
	if reloaded.Status != DomainStatusSuspended || reloaded.SuspendedReason != "abuse report" {
		t.Errorf("SetDomainStatus not persisted: status=%q reason=%q", reloaded.Status, reloaded.SuspendedReason)
	}
}

func TestDomainSSLLifecycle(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "ssldomainsowner", "ssldomainsowner@example.com")
	d := newTestDomain(t, store, OwnerUser, owner.ID, "ssl-domain.example")

	if err := store.SetDomainSSLPending(ctx, d.ID, ChallengeHTTP01); err != nil {
		t.Fatalf("SetDomainSSLPending: %v", err)
	}
	reloaded, err := store.DomainByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("DomainByID: %v", err)
	}
	if !reloaded.SSLEnabled || reloaded.SSLStatus != SSLStatusPending {
		t.Errorf("SetDomainSSLPending: enabled=%v status=%q", reloaded.SSLEnabled, reloaded.SSLStatus)
	}

	now := time.Now().Unix()
	if err := store.SetDomainSSLIssued(ctx, d.ID, "cert-pem", "key-pem", now, now+90*24*3600); err != nil {
		t.Fatalf("SetDomainSSLIssued: %v", err)
	}
	reloaded, err = store.DomainByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("DomainByID: %v", err)
	}
	if reloaded.SSLStatus != SSLStatusActive || reloaded.SSLCertPEM != "cert-pem" || reloaded.SSLKeyPEM != "key-pem" {
		t.Errorf("SetDomainSSLIssued not persisted correctly: %+v", reloaded)
	}

	if err := store.SetDomainSSLError(ctx, d.ID, "issuance failed"); err != nil {
		t.Fatalf("SetDomainSSLError: %v", err)
	}
	reloaded, err = store.DomainByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("DomainByID: %v", err)
	}
	if reloaded.SSLStatus != SSLStatusError || reloaded.SSLLastError != "issuance failed" {
		t.Errorf("SetDomainSSLError not persisted: status=%q err=%q", reloaded.SSLStatus, reloaded.SSLLastError)
	}

	if err := store.DisableDomainSSL(ctx, d.ID); err != nil {
		t.Fatalf("DisableDomainSSL: %v", err)
	}
	reloaded, err = store.DomainByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("DomainByID: %v", err)
	}
	if reloaded.SSLEnabled || reloaded.SSLStatus != SSLStatusNone || reloaded.SSLCertPEM != "" {
		t.Errorf("DisableDomainSSL did not clear SSL state: %+v", reloaded)
	}
}

func TestListDomainsForRenewal(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "renewaldomainsowner", "renewaldomainsowner@example.com")
	d := &CustomDomain{
		OwnerType:          OwnerUser,
		OwnerID:            owner.ID,
		Domain:             "renewal-domain.example",
		VerificationStatus: VerificationVerified,
		VerificationToken:  "cashp-verify=faketoken",
		SSLEnabled:         true,
		Status:             DomainStatusActive,
	}
	if _, err := store.CreateDomain(ctx, d); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	if d.ID == 0 {
		t.Fatal("CreateDomain left CustomDomain.ID at 0 — id regression")
	}

	now := time.Now().Unix()
	soon := now + 3600
	if err := store.SetDomainSSLIssued(ctx, d.ID, "cert", "key", now, soon); err != nil {
		t.Fatalf("SetDomainSSLIssued: %v", err)
	}

	due, err := store.ListDomainsForRenewal(ctx, now+7200, 0)
	if err != nil {
		t.Fatalf("ListDomainsForRenewal: %v", err)
	}
	found := false
	for _, dd := range due {
		if dd.ID == d.ID {
			found = true
		}
	}
	if !found {
		t.Error("ListDomainsForRenewal did not surface a domain expiring within the window")
	}
}

func TestDeleteDomainScopedByOwner(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "deletedomainowner", "deletedomainowner@example.com")
	attacker := newTestUser(t, store, "deletedomainattacker", "deletedomainattacker@example.com")
	d := newTestDomain(t, store, OwnerUser, owner.ID, "delete-domain.example")

	// Cross-owner delete must be a no-op.
	if err := store.DeleteDomain(ctx, OwnerUser, attacker.ID, d.ID); err != nil {
		t.Fatalf("DeleteDomain(wrong owner): %v", err)
	}
	if _, err := store.DomainByID(ctx, d.ID); err != nil {
		t.Error("cross-owner DeleteDomain removed a domain it did not own")
	}

	if err := store.DeleteDomain(ctx, OwnerUser, owner.ID, d.ID); err != nil {
		t.Fatalf("DeleteDomain(owner): %v", err)
	}
	if _, err := store.DomainByID(ctx, d.ID); err == nil {
		t.Error("domain still readable after owner-scoped DeleteDomain")
	}

	// Idempotency: deleting an already-deleted domain must not error.
	if err := store.DeleteDomain(ctx, OwnerUser, owner.ID, d.ID); err != nil {
		t.Errorf("DeleteDomain (already deleted): %v", err)
	}
}

func TestPurgeStaleDomains(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "staledomainsowner", "staledomainsowner@example.com")

	stale := &CustomDomain{
		OwnerType:          OwnerUser,
		OwnerID:             owner.ID,
		Domain:             "stale-domain.example",
		VerificationStatus: VerificationPending,
		Status:             DomainStatusPending,
		CreatedAt:          1,
	}
	if _, err := store.CreateDomain(ctx, stale); err != nil {
		t.Fatalf("CreateDomain(stale): %v", err)
	}
	fresh := newTestDomain(t, store, OwnerUser, owner.ID, "fresh-domain.example")

	n, err := store.PurgeStaleDomains(ctx, time.Now().Unix())
	if err != nil {
		t.Fatalf("PurgeStaleDomains: %v", err)
	}
	if n != 1 {
		t.Errorf("PurgeStaleDomains purged %d rows, want 1", n)
	}
	if _, err := store.DomainByID(ctx, stale.ID); err == nil {
		t.Error("stale never-verified domain survived PurgeStaleDomains")
	}
	if _, err := store.DomainByID(ctx, fresh.ID); err != nil {
		t.Error("fresh domain was purged by PurgeStaleDomains")
	}

	// Idempotency: purging again must not error.
	n2, err := store.PurgeStaleDomains(ctx, time.Now().Unix())
	if err != nil {
		t.Errorf("PurgeStaleDomains (second run): %v", err)
	}
	if n2 != 0 {
		t.Errorf("PurgeStaleDomains (second run) purged %d rows, want 0", n2)
	}
}

func TestRecordDomainAudit(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "auditdomainsowner", "auditdomainsowner@example.com")
	d := newTestDomain(t, store, OwnerUser, owner.ID, "audit-domain.example")

	if err := store.RecordDomainAudit(ctx, d.ID, "verified", OwnerUser, owner.ID, "{}"); err != nil {
		t.Errorf("RecordDomainAudit: %v", err)
	}
}
