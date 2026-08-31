package guard

import (
	"testing"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// tenantUser is an active end user of tenant t1 holding one explicit grant.
func tenantUser() Subject {
	return Subject{
		UserID:   "u1",
		TenantID: "t1",
		Role:     RoleEndUser,
		Active:   true,
		Grants: []Grant{
			{ResourceType: ResourceSite, ResourceID: "s1", Actions: []Action{ActionRead, ActionUpdate}},
		},
	}
}

// tenantAdmin is an active account admin of tenant t1.
func tenantAdmin() Subject {
	return Subject{UserID: "a1", TenantID: "t1", Role: RoleAccountAdmin, Active: true}
}

// globalAdmin is an active global administrator.
func globalAdmin() Subject {
	return Subject{UserID: "g1", Role: RoleGlobalAdmin, Active: true}
}

func TestAuthorizeDeniesAnonymousAndInactiveSubjects(t *testing.T) {
	site := Resource{Type: ResourceSite, ID: "s1", TenantID: "t1"}

	if err := Authorize(Subject{}, ActionRead, site); err == nil {
		t.Fatal("Authorize permitted the zero subject")
	} else if DenialReason(err) != ReasonSubjectInvalid {
		t.Fatalf("expected subject_invalid, got %q", DenialReason(err))
	}

	suspended := tenantAdmin()
	suspended.Active = false
	if err := Authorize(suspended, ActionRead, site); DenialReason(err) != ReasonSubjectInactive {
		t.Fatalf("expected subject_inactive, got %q", DenialReason(err))
	}

	badRole := tenantAdmin()
	badRole.Role = Role("superuser")
	if err := Authorize(badRole, ActionRead, site); DenialReason(err) != ReasonSubjectInvalid {
		t.Fatalf("expected subject_invalid for an unknown role, got %q", DenialReason(err))
	}
}

func TestAuthorizeDeniesCrossTenantAccessAsNotFound(t *testing.T) {
	foreign := Resource{Type: ResourceSite, ID: "s9", TenantID: "t2"}

	for _, s := range []Subject{tenantAdmin(), tenantUser()} {
		err := Authorize(s, ActionRead, foreign)
		if err == nil {
			t.Fatalf("Authorize permitted a cross-tenant read for %s", s.UserID)
		}
		if DenialReason(err) != ReasonCrossTenant {
			t.Fatalf("expected cross_tenant, got %q", DenialReason(err))
		}
		// A foreign resource must be indistinguishable from a missing one.
		if code := AppErrorFor(err).Code; code != apperr.CodeNotFound {
			t.Fatalf("cross-tenant denial leaked existence with code %q", code)
		}
	}
}

func TestAuthorizeDeniesUnscopedResources(t *testing.T) {
	unscoped := Resource{Type: ResourceSite, ID: "s1"}
	if err := Authorize(tenantAdmin(), ActionRead, unscoped); err == nil {
		t.Fatal("Authorize permitted access to a resource with no tenant scope")
	}

	unscopedSubject := tenantAdmin()
	unscopedSubject.TenantID = ""
	if err := Authorize(unscopedSubject, ActionRead, Resource{Type: ResourceSite, ID: "s1", TenantID: "t1"}); err == nil {
		t.Fatal("Authorize permitted an account admin with no tenant scope")
	}
}

func TestAuthorizeDeniesPrivilegeEscalationByEndUser(t *testing.T) {
	user := tenantUser()

	// Administer is never held by an end user, even inside their own tenant.
	if err := Authorize(user, ActionAdminister, Resource{Type: ResourceSite, ID: "s1", TenantID: "t1"}); err == nil {
		t.Fatal("Authorize granted administer to an end user")
	}

	// A grant for one resource must not cover another.
	if err := Authorize(user, ActionRead, Resource{Type: ResourceSite, ID: "s2", TenantID: "t1"}); err == nil {
		t.Fatal("Authorize let a grant for s1 cover s2")
	}

	// A grant for read must not cover delete.
	if err := Authorize(user, ActionDelete, Resource{Type: ResourceSite, ID: "s1", TenantID: "t1"}); err == nil {
		t.Fatal("Authorize let a read grant cover delete")
	}

	// A grant for one type must not cover another.
	if err := Authorize(user, ActionRead, Resource{Type: ResourceDatabase, ID: "s1", TenantID: "t1"}); err == nil {
		t.Fatal("Authorize let a site grant cover a database")
	}

	// The one combination the grant actually names must pass.
	if err := Authorize(user, ActionUpdate, Resource{Type: ResourceSite, ID: "s1", TenantID: "t1"}); err != nil {
		t.Fatalf("Authorize denied the granted action: %v", err)
	}
}

func TestAuthorizeKeepsSecurityStackOutOfTenantReach(t *testing.T) {
	for _, resourceType := range []string{ResourceSecurityStack, ResourcePlatformSetting} {
		target := Resource{Type: resourceType, ID: "x", TenantID: "t1"}
		for _, s := range []Subject{tenantAdmin(), tenantUser()} {
			if err := Authorize(s, ActionUpdate, target); err == nil {
				t.Fatalf("%s let %s change %s", s.Role, s.UserID, resourceType)
			} else if DenialReason(err) != ReasonPlatformControlled {
				t.Fatalf("expected platform_controlled, got %q", DenialReason(err))
			}
			if err := Authorize(s, ActionRead, target); err == nil {
				t.Fatalf("%s let %s read %s", s.Role, s.UserID, resourceType)
			}
		}
		if err := Authorize(globalAdmin(), ActionUpdate, target); err != nil {
			t.Fatalf("global admin was denied %s: %v", resourceType, err)
		}
	}
}

func TestAuthorizeMakesPrimaryAdminFlagImmutableForEveryone(t *testing.T) {
	flag := Resource{Type: ResourcePrimaryAdminFlag, ID: "primary"}
	primary := globalAdmin()
	primary.PrimaryAdmin = true

	for _, action := range []Action{ActionCreate, ActionUpdate, ActionDelete, ActionAdminister} {
		for _, s := range []Subject{primary, globalAdmin(), tenantAdmin(), tenantUser()} {
			err := Authorize(s, action, flag)
			if err == nil {
				t.Fatalf("%s was permitted to %s the primary admin flag", s.UserID, action)
			}
			if DenialReason(err) != ReasonPrimaryAdminImmutable {
				t.Fatalf("expected primary_admin_immutable, got %q", DenialReason(err))
			}
		}
	}
}

func TestAuthorizeBlocksPeerAdministratorCredentials(t *testing.T) {
	admin := globalAdmin()
	peer := Resource{Type: ResourceAdminCredential, ID: "c2", OwnerUserID: "g2", OwnerRole: RoleGlobalAdmin}

	if err := Authorize(admin, ActionRead, peer); err == nil {
		t.Fatal("a global admin read another administrator's credentials")
	} else if DenialReason(err) != ReasonPeerAdminCredential {
		t.Fatalf("expected peer_admin_credential, got %q", DenialReason(err))
	}

	own := Resource{Type: ResourceAdminCredential, ID: "c1", OwnerUserID: "g1", OwnerRole: RoleGlobalAdmin}
	if err := Authorize(admin, ActionUpdate, own); err != nil {
		t.Fatalf("a global admin was denied their own credentials: %v", err)
	}
}

func TestAuthorizeRejectsUnknownActions(t *testing.T) {
	if err := Authorize(globalAdmin(), Action("sudo"), Resource{Type: ResourceSite, TenantID: "t1"}); err == nil {
		t.Fatal("Authorize accepted an undefined action")
	}
	if err := Authorize(globalAdmin(), ActionRead, Resource{}); err == nil {
		t.Fatal("Authorize accepted a resource with no type")
	}
}

func TestRequireOwnerRejectsSameTenantNonOwner(t *testing.T) {
	other := Resource{Type: ResourceSupportTicket, ID: "tk1", TenantID: "t1", OwnerUserID: "u2"}
	if err := RequireOwner(tenantAdmin(), other); err == nil {
		t.Fatal("RequireOwner let an account admin act as another user's owner")
	}

	mine := Resource{Type: ResourceSite, ID: "s1", TenantID: "t1", OwnerUserID: "u1"}
	if err := RequireOwner(tenantUser(), mine); err != nil {
		t.Fatalf("RequireOwner denied the actual owner: %v", err)
	}

	ownerless := Resource{Type: ResourceSite, ID: "s1", TenantID: "t1"}
	if err := RequireOwner(tenantAdmin(), ownerless); err == nil {
		t.Fatal("RequireOwner accepted a resource with no owner")
	}
}

func TestRequireSameTenantDeniesMissingScope(t *testing.T) {
	if err := RequireSameTenant(tenantAdmin(), "t2"); err == nil {
		t.Fatal("RequireSameTenant permitted a foreign tenant")
	}
	if err := RequireSameTenant(tenantAdmin(), ""); err == nil {
		t.Fatal("RequireSameTenant treated an empty tenant as a wildcard")
	}
	if err := RequireSameTenant(tenantAdmin(), "t1"); err != nil {
		t.Fatalf("RequireSameTenant denied the owning tenant: %v", err)
	}
	if err := RequireSameTenant(globalAdmin(), "t2"); err != nil {
		t.Fatalf("RequireSameTenant denied a global admin: %v", err)
	}
}

func TestTenantFilterScopesQueries(t *testing.T) {
	unscoped := tenantAdmin()
	unscoped.TenantID = ""
	if _, err := NewTenantFilter(unscoped); err == nil {
		t.Fatal("NewTenantFilter produced an unscoped filter for a non-admin")
	}

	filter, err := NewTenantFilter(tenantAdmin())
	if err != nil {
		t.Fatalf("NewTenantFilter failed: %v", err)
	}
	if filter.All {
		t.Fatal("NewTenantFilter gave an account admin installation-wide scope")
	}
	if filter.Matches("t2") || filter.Matches("") {
		t.Fatal("TenantFilter matched a foreign or empty tenant")
	}
	if !filter.Matches("t1") {
		t.Fatal("TenantFilter rejected its own tenant")
	}

	fragment, args, err := filter.SQL("tenant_id")
	if err != nil {
		t.Fatalf("TenantFilter.SQL failed: %v", err)
	}
	if fragment != "tenant_id = ?" || len(args) != 1 || args[0] != "t1" {
		t.Fatalf("TenantFilter.SQL produced %q with %v", fragment, args)
	}

	if _, _, err := filter.SQL("tenant_id = 1 OR 1=1 --"); err == nil {
		t.Fatal("TenantFilter.SQL accepted an injected column name")
	}

	adminFilter, err := NewTenantFilter(globalAdmin())
	if err != nil {
		t.Fatalf("NewTenantFilter failed for a global admin: %v", err)
	}
	if !adminFilter.All || !adminFilter.Matches("anything") {
		t.Fatal("a global admin filter was not installation-wide")
	}
}

func TestParseRoleRejectsUnknownRoles(t *testing.T) {
	for _, value := range []string{"", "root", "administrator", "admin", "global_admin;--"} {
		if _, err := ParseRole(value); err == nil {
			t.Fatalf("ParseRole accepted %q", value)
		}
	}
	if _, err := ParseRole(string(RoleEndUser)); err != nil {
		t.Fatalf("ParseRole rejected a defined role: %v", err)
	}
}

func TestDenyErrorNeverLeaksDetailToTheClient(t *testing.T) {
	denial := Deny(ReasonCrossTenant, "subject tenant t1 does not own site/s9")
	appError := denial.AppError()
	if appError.Message == denial.Detail {
		t.Fatal("the client-visible message carried the log-only detail")
	}
	if appError.Message != apperr.DefaultMessage(apperr.CodeNotFound) {
		t.Fatalf("expected the generic default message, got %q", appError.Message)
	}
}
