package auth

import "testing"

func TestPublicSessionsFlagsCurrent(t *testing.T) {
	rows := []*Session{
		{ID: 1, IPAddress: "10.0.0.1"},
		{ID: 2, IPAddress: "10.0.0.2"},
	}
	out := publicSessions(rows, 2)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].Current {
		t.Error("session 1 flagged current, want false")
	}
	if !out[1].Current {
		t.Error("session 2 not flagged current, want true")
	}
}

func TestPublicSessionsEmpty(t *testing.T) {
	out := publicSessions(nil, 1)
	if out == nil {
		t.Error("publicSessions(nil, ...) must return an empty slice, not nil, so JSON encodes [] not null")
	}
	if len(out) != 0 {
		t.Errorf("len = %d, want 0", len(out))
	}
}

func TestPublicMembers(t *testing.T) {
	rows := []*OrgMember{{UserID: 1, Username: "alice", Role: OrgRoleOwner}}
	out := publicMembers(rows)
	if len(out) != 1 || out[0].Username != "alice" || out[0].Role != OrgRoleOwner {
		t.Errorf("publicMembers = %+v", out)
	}
}

func TestPublicInvitesNeverIncludesCode(t *testing.T) {
	rows := []*Invite{{ID: 1, Code: "super-secret-code", Role: OrgRoleMember}}
	out := publicInvites(rows)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if out[0].Code != "" {
		t.Errorf("publicInvites leaked the plaintext code: %q", out[0].Code)
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	db := newAuthTestDB(t)
	svc, err := New(Options{Store: NewStore(db), Config: DefaultConfig()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func TestPublicDomainShowsRecordWhilePending(t *testing.T) {
	svc := newTestService(t)
	d := &CustomDomain{
		ID:                 1,
		Domain:             "example.com",
		VerificationStatus: VerificationPending,
		VerificationToken:  "cashp-verify=abc123",
	}
	pub := svc.publicDomain(d)
	if pub.RecordName == "" || pub.RecordValue == "" {
		t.Error("a pending domain must expose RecordName/RecordValue")
	}
}

func TestPublicDomainHidesRecordOnceVerified(t *testing.T) {
	svc := newTestService(t)
	d := &CustomDomain{
		ID:                 1,
		Domain:             "example.com",
		VerificationStatus: VerificationVerified,
		VerificationToken:  "cashp-verify=abc123",
	}
	pub := svc.publicDomain(d)
	if pub.RecordName != "" || pub.RecordValue != "" {
		t.Errorf("a verified domain must not echo its verification token, got name=%q value=%q", pub.RecordName, pub.RecordValue)
	}
}
