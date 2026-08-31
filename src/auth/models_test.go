package auth

import (
	"testing"
	"time"
)

func TestUserLocked(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name        string
		lockedUntil int64
		want        bool
	}{
		{"never locked", 0, false},
		{"locked in the past", now - 100, false},
		{"locked in the future", now + 100, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := &User{LockedUntil: c.lockedUntil}
			if got := u.Locked(); got != c.want {
				t.Errorf("Locked() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestUserPublicIncludesPrivateFields(t *testing.T) {
	u := &User{ID: 1, Username: "alice", Email: "alice@example.com", Role: RoleUser}
	pub := u.Public()
	if pub.Email != "alice@example.com" {
		t.Error("Public() self-view must include Email")
	}
	if pub.Role != RoleUser {
		t.Error("Public() self-view must include Role")
	}
}

func TestUserProfileOmitsPrivateFields(t *testing.T) {
	u := &User{ID: 1, Username: "alice", Email: "alice@example.com", Role: RoleAdmin}
	prof := u.Profile()
	if prof.Email != "" {
		t.Errorf("Profile() third-party view must omit Email, got %q", prof.Email)
	}
	if prof.Role != "" {
		t.Errorf("Profile() third-party view must omit Role, got %q", prof.Role)
	}
	if prof.Username != "alice" {
		t.Error("Profile() must still include public Username")
	}
}

func TestSessionExpired(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name string
		exp  int64
		want bool
	}{
		{"far future", now + 1000, false},
		{"past", now - 1000, true},
	}
	for _, c := range cases {
		s := &Session{ExpiresAt: c.exp}
		if got := s.Expired(); got != c.want {
			t.Errorf("Session{ExpiresAt:%d}.Expired() = %v, want %v", c.exp, got, c.want)
		}
	}
}

func TestTokenExpiredAndUsable(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name        string
		expiresAt   int64
		revoked     bool
		wantExpired bool
		wantUsable  bool
	}{
		{"no expiry, not revoked", 0, false, false, true},
		{"future expiry, not revoked", now + 1000, false, false, true},
		{"past expiry, not revoked", now - 1000, false, true, false},
		{"no expiry, revoked", 0, true, false, false},
		{"future expiry, revoked", now + 1000, true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tok := &Token{ExpiresAt: c.expiresAt, Revoked: c.revoked}
			if got := tok.Expired(); got != c.wantExpired {
				t.Errorf("Expired() = %v, want %v", got, c.wantExpired)
			}
			if got := tok.Usable(); got != c.wantUsable {
				t.Errorf("Usable() = %v, want %v", got, c.wantUsable)
			}
		})
	}
}

func TestInviteUsable(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name      string
		revoked   bool
		expiresAt int64
		maxUses   int
		useCount  int
		want      bool
	}{
		{"fresh, unlimited uses, no expiry", false, 0, 0, 0, true},
		{"revoked", true, 0, 0, 0, false},
		{"expired", false, now - 100, 0, 0, false},
		{"not yet expired", false, now + 100, 0, 0, true},
		{"uses exhausted", false, 0, 1, 1, false},
		{"uses remaining", false, 0, 5, 4, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inv := &Invite{Revoked: c.revoked, ExpiresAt: c.expiresAt, MaxUses: c.maxUses, UseCount: c.useCount}
			if got := inv.Usable(); got != c.want {
				t.Errorf("Usable() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestOrgRolePredicates(t *testing.T) {
	cases := []struct {
		role           string
		wantManage     bool
		wantManageOrg  bool
		wantDelete     bool
	}{
		{OrgRoleOwner, true, true, true},
		{OrgRoleAdmin, true, true, false},
		{OrgRoleMember, false, false, false},
		{"", false, false, false},
	}
	for _, c := range cases {
		if got := CanManageMembers(c.role); got != c.wantManage {
			t.Errorf("CanManageMembers(%q) = %v, want %v", c.role, got, c.wantManage)
		}
		if got := CanManageOrg(c.role); got != c.wantManageOrg {
			t.Errorf("CanManageOrg(%q) = %v, want %v", c.role, got, c.wantManageOrg)
		}
		if got := CanDeleteOrg(c.role); got != c.wantDelete {
			t.Errorf("CanDeleteOrg(%q) = %v, want %v", c.role, got, c.wantDelete)
		}
	}
}

func TestOrgPublic(t *testing.T) {
	o := &Org{ID: 1, Slug: "acme", Name: "Acme"}
	pub := o.Public(3, OrgRoleAdmin)
	if pub.MemberCount != 3 {
		t.Errorf("MemberCount = %d, want 3", pub.MemberCount)
	}
	if pub.YourRole != OrgRoleAdmin {
		t.Errorf("YourRole = %q, want %q", pub.YourRole, OrgRoleAdmin)
	}
	if pub.Slug != "acme" {
		t.Errorf("Slug = %q, want acme", pub.Slug)
	}
}
