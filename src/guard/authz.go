package guard

import (
	"strings"

	"github.com/webappsgo/cashp/src/security"
)

// Role is a cashp RBAC role. The set is closed: a string outside it is not
// a weaker role, it is an unauthenticated caller.
type Role string

// The three roles defined by the business logic. There is no implicit
// ordering between them beyond what Authorize encodes; nothing in this
// package derives a role from registration order or account age.
const (
	// RoleGlobalAdmin governs the whole installation or cluster.
	RoleGlobalAdmin Role = "global_admin"
	// RoleAccountAdmin governs a single tenant.
	RoleAccountAdmin Role = "account_admin"
	// RoleEndUser governs only the resources an account admin explicitly granted.
	RoleEndUser Role = "end_user"
)

// Valid reports whether r is one of the three defined roles.
func (r Role) Valid() bool {
	switch r {
	case RoleGlobalAdmin, RoleAccountAdmin, RoleEndUser:
		return true
	default:
		return false
	}
}

// ParseRole converts a stored role string into a Role. An unrecognized
// value is an error rather than a default role, so a corrupted or
// attacker-influenced row can never be read as a privilege.
func ParseRole(s string) (Role, error) {
	r := Role(strings.TrimSpace(strings.ToLower(s)))
	if !r.Valid() {
		return "", Deny(ReasonRoleUnknown, "unrecognized role "+s)
	}
	return r, nil
}

// Action is an operation attempted against a resource.
type Action string

// The closed action set. Anything a caller cannot express as one of these
// is denied rather than mapped onto the nearest match.
const (
	// ActionRead retrieves or lists a resource.
	ActionRead Action = "read"
	// ActionCreate provisions a new resource.
	ActionCreate Action = "create"
	// ActionUpdate modifies an existing resource.
	ActionUpdate Action = "update"
	// ActionDelete removes a resource.
	ActionDelete Action = "delete"
	// ActionAdminister changes policy, membership, or roles.
	ActionAdminister Action = "administer"
)

// Valid reports whether a is one of the defined actions.
func (a Action) Valid() bool {
	switch a {
	case ActionRead, ActionCreate, ActionUpdate, ActionDelete, ActionAdminister:
		return true
	default:
		return false
	}
}

// Mutating reports whether a changes state.
func (a Action) Mutating() bool {
	return a.Valid() && a != ActionRead
}

// Resource type names. They mirror the core entities in the business
// logic's data model, plus the three platform-owned pseudo-resources that
// exist so their special rules are expressible.
const (
	// ResourceSite is a hosted virtual host.
	ResourceSite = "site"
	// ResourceApp is a PaaS application.
	ResourceApp = "app"
	// ResourceContainer is a tenant-defined container workload.
	ResourceContainer = "container"
	// ResourceVM is a tenant-defined virtual machine.
	ResourceVM = "vm"
	// ResourceDatabase is a provisioned database instance.
	ResourceDatabase = "database"
	// ResourceMailbox is a mailbox on a tenant mail domain.
	ResourceMailbox = "mailbox"
	// ResourceDNSZone is an authoritative DNS zone.
	ResourceDNSZone = "dns_zone"
	// ResourceBackupJob is a scheduled backup and its retention policy.
	ResourceBackupJob = "backup_job"
	// ResourceBillingAccount is a tenant's billing account and balance.
	ResourceBillingAccount = "billing_account"
	// ResourceInvoice is an issued invoice.
	ResourceInvoice = "invoice"
	// ResourceSupportTicket is a support ticket and its messages.
	ResourceSupportTicket = "support_ticket"
	// ResourceUser is a user account record.
	ResourceUser = "user"
	// ResourceTenant is a hosting account record.
	ResourceTenant = "tenant"
	// ResourceSecurityStack is the always-on AV, IPS, and WAF configuration.
	ResourceSecurityStack = "security_stack"
	// ResourcePlatformSetting is installation-wide or cluster-wide configuration.
	ResourcePlatformSetting = "platform_setting"
	// ResourcePrimaryAdminFlag is the tamper-proof primary-global-admin marker.
	ResourcePrimaryAdminFlag = "primary_admin_flag"
	// ResourceAdminCredential is a global administrator's password, API token, or 2FA secret.
	ResourceAdminCredential = "admin_credential"
)

// platformOwned are the resource types the platform controls outright. A
// tenant of any billing tier can neither read nor change them, which is
// what keeps the always-on security stack from being weakened for one
// account on a node other accounts share.
var platformOwned = map[string]struct{}{
	ResourceSecurityStack:   {},
	ResourcePlatformSetting: {},
}

// Grant is a single explicit permission an account admin issued to an end
// user. There is no wildcard form: a grant names one resource type, one
// resource id, and the exact actions allowed on it.
type Grant struct {
	// ResourceType is the type the grant applies to.
	ResourceType string
	// ResourceID is the single resource the grant applies to. An empty id grants only collection-level create.
	ResourceID string
	// Actions are the operations the grant permits.
	Actions []Action
}

// Permits reports whether the grant covers this exact type, id, and action.
func (g Grant) Permits(resourceType, resourceID string, action Action) bool {
	if g.ResourceType != resourceType || g.ResourceID != resourceID {
		return false
	}
	for _, a := range g.Actions {
		if a == action {
			return true
		}
	}
	return false
}

// Subject is the authenticated caller an authorization decision is made
// for. The zero Subject is an anonymous caller and is denied everything.
type Subject struct {
	// UserID identifies the caller.
	UserID string
	// TenantID is the hosting account the caller belongs to. It is empty only for a global admin.
	TenantID string
	// Role is the caller's RBAC role.
	Role Role
	// Active is false for a suspended, locked, or pending-deletion account.
	Active bool
	// PrimaryAdmin marks the tamper-proof primary global admin.
	PrimaryAdmin bool
	// Grants are the explicit per-resource permissions issued to an end user.
	Grants []Grant
}

// Resource is the object an action targets. TenantID must be populated for
// every tenant-scoped type, including on a create, where it is the tenant
// the new resource will belong to.
type Resource struct {
	// Type is one of the Resource* constants.
	Type string
	// ID identifies the resource. It is empty on a create.
	ID string
	// TenantID is the owning hosting account.
	TenantID string
	// OwnerUserID is the user the resource belongs to, where ownership is per-user rather than per-tenant.
	OwnerUserID string
	// OwnerRole is the role of OwnerUserID, used for the peer-administrator credential rule.
	OwnerRole Role
	// PlatformControlled marks a resource the platform owns even though its type is not inherently platform-owned.
	PlatformControlled bool
}

// Authorize is the single deny-by-default authorization decision every
// resource access must pass. It returns nil when the action is permitted
// and a *DenyError otherwise; it never returns a partial or advisory
// result, so a caller that ignores the error is a visible bug rather than
// a silent bypass.
//
// The evaluation order is deliberate: absolute prohibitions that bind even
// a global admin are checked first, then platform ownership, then the
// per-role rules.
func Authorize(s Subject, action Action, r Resource) error {
	if s.UserID == "" || !s.Role.Valid() {
		return Deny(ReasonSubjectInvalid, "subject has no identity or an unusable role")
	}
	if !s.Active {
		return Deny(ReasonSubjectInactive, "subject "+s.UserID+" is not active")
	}
	if !action.Valid() {
		return Deny(ReasonActionUnknown, "action "+string(action)+" is not defined")
	}
	if r.Type == "" {
		return Deny(ReasonResourceInvalid, "resource has no type")
	}

	// The primary-global-admin marker is immutable through every surface
	// for every caller, including the primary admin itself; recovery goes
	// through the setup-token flow, never through a write here.
	if r.Type == ResourcePrimaryAdminFlag && action.Mutating() {
		return Deny(ReasonPrimaryAdminImmutable, "primary admin flag is not writable")
	}

	// One administrator may never read or change another administrator's
	// credentials, so a compromised admin session cannot pivot across the
	// rest of the admin set.
	if r.Type == ResourceAdminCredential && !security.ConstantTimeEqualString(r.OwnerUserID, s.UserID) {
		return Deny(ReasonPeerAdminCredential, "credentials of "+r.OwnerUserID+" are not visible to "+s.UserID)
	}

	if _, owned := platformOwned[r.Type]; owned || r.PlatformControlled {
		if s.Role != RoleGlobalAdmin {
			return Deny(ReasonPlatformControlled, "resource "+r.Type+" is platform-controlled")
		}
		return nil
	}

	switch s.Role {
	case RoleGlobalAdmin:
		return nil
	case RoleAccountAdmin:
		if err := requireSameTenant(s, r); err != nil {
			return err
		}
		return nil
	case RoleEndUser:
		if err := requireSameTenant(s, r); err != nil {
			return err
		}
		if action == ActionAdminister {
			return Deny(ReasonNoGrant, "end users never hold administer")
		}
		for _, g := range s.Grants {
			if g.Permits(r.Type, r.ID, action) {
				return nil
			}
		}
		return Deny(ReasonNoGrant, "no grant covers "+r.Type+"/"+r.ID+" for "+string(action))
	}

	// Unreachable while Role.Valid stays exhaustive; kept so a future role
	// added to the constant set without a case here denies rather than
	// falling through to an allow.
	return Deny(ReasonRoleUnknown, "no rule for role "+string(s.Role))
}

// requireSameTenant enforces the tenant boundary. A missing tenant on
// either side is a denial rather than a wildcard, which is what stops an
// unscoped record from being readable by everyone.
func requireSameTenant(s Subject, r Resource) *DenyError {
	if s.TenantID == "" {
		return Deny(ReasonCrossTenant, "subject "+s.UserID+" has no tenant scope")
	}
	if r.TenantID == "" {
		return Deny(ReasonCrossTenant, "resource "+r.Type+"/"+r.ID+" has no tenant scope")
	}
	if !security.ConstantTimeEqualString(s.TenantID, r.TenantID) {
		return Deny(ReasonCrossTenant, "subject tenant "+s.TenantID+" does not own "+r.Type+"/"+r.ID)
	}
	return nil
}

// RequireSameTenant is the standalone tenant-boundary check for call sites
// that already authorized the action and only need to confirm ownership of
// a record they just loaded.
func RequireSameTenant(s Subject, tenantID string) error {
	if s.UserID == "" || !s.Role.Valid() {
		return Deny(ReasonSubjectInvalid, "subject has no identity or an unusable role")
	}
	if !s.Active {
		return Deny(ReasonSubjectInactive, "subject "+s.UserID+" is not active")
	}
	if s.Role == RoleGlobalAdmin {
		return nil
	}
	if err := requireSameTenant(s, Resource{Type: "record", TenantID: tenantID}); err != nil {
		return err
	}
	return nil
}

// RequireOwner enforces per-user ownership on top of the tenant boundary,
// for resources an account admin may see but only their owner may change.
func RequireOwner(s Subject, r Resource) error {
	if err := Authorize(s, ActionRead, r); err != nil {
		return err
	}
	if s.Role == RoleGlobalAdmin {
		return nil
	}
	if r.OwnerUserID == "" {
		return Deny(ReasonResourceInvalid, "resource "+r.Type+"/"+r.ID+" has no owner")
	}
	if !security.ConstantTimeEqualString(r.OwnerUserID, s.UserID) {
		return Deny(ReasonCrossTenant, "subject "+s.UserID+" does not own "+r.Type+"/"+r.ID)
	}
	return nil
}

// TenantFilter is the mandatory scope every tenant-scoped query must
// carry. Obtaining one is the only supported way to build such a query, so
// a forgotten tenant predicate shows up as a missing NewTenantFilter call
// rather than as a silent cross-tenant read.
type TenantFilter struct {
	// All is true only for a global admin, whose scope is the whole installation.
	All bool
	// TenantID is the single tenant the query is confined to when All is false.
	TenantID string
}

// NewTenantFilter derives the query scope for a subject. A non-admin
// subject with no tenant is an error, never an unscoped query.
func NewTenantFilter(s Subject) (TenantFilter, error) {
	if s.UserID == "" || !s.Role.Valid() {
		return TenantFilter{}, Deny(ReasonSubjectInvalid, "subject has no identity or an unusable role")
	}
	if !s.Active {
		return TenantFilter{}, Deny(ReasonSubjectInactive, "subject "+s.UserID+" is not active")
	}
	if s.Role == RoleGlobalAdmin {
		return TenantFilter{All: true}, nil
	}
	if s.TenantID == "" {
		return TenantFilter{}, Deny(ReasonCrossTenant, "subject "+s.UserID+" has no tenant scope")
	}
	return TenantFilter{TenantID: s.TenantID}, nil
}

// Matches reports whether a loaded row is inside the filter's scope. Call
// it on any record fetched by an identifier rather than by a scoped query.
func (f TenantFilter) Matches(tenantID string) bool {
	if f.All {
		return true
	}
	if f.TenantID == "" || tenantID == "" {
		return false
	}
	return security.ConstantTimeEqualString(f.TenantID, tenantID)
}

// SQL renders the filter as a parameterized predicate for the named
// column, returning the fragment and its bind arguments. The column name
// is validated as an SQL identifier, so this can never become a string
// concatenation injection point, and the value always binds.
func (f TenantFilter) SQL(column string) (string, []any, error) {
	if err := ValidateSQLIdentifier("column", column); err != nil {
		return "", nil, err
	}
	if f.All {
		return "1 = 1", nil, nil
	}
	if f.TenantID == "" {
		return "", nil, Deny(ReasonCrossTenant, "tenant filter has no scope")
	}
	return column + " = ?", []any{f.TenantID}, nil
}
