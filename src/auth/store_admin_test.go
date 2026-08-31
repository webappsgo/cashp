package auth

import (
	"context"
	"testing"

	"github.com/webappsgo/cashp/src/database"
)

// newTestAdmin creates a Server Admin row and fails the test if the returned
// id is zero — the regression this whole pass exists to catch.
func newTestAdmin(t *testing.T, store *Store, username, email string) *Admin {
	t.Helper()
	a := &Admin{
		Username:     username,
		Email:        email,
		PasswordHash: "argon2id$fake-hash-for-testing",
	}
	id, err := store.CreateAdmin(context.Background(), a)
	if err != nil {
		t.Fatalf("CreateAdmin: %v", err)
	}
	if id == 0 {
		t.Fatal("CreateAdmin returned id 0")
	}
	if a.ID == 0 {
		t.Fatal("CreateAdmin left Admin.ID at 0")
	}
	return a
}

func TestCreateAdminAndLookups(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	a := newTestAdmin(t, store, "root-admin", "root@example.com")

	byID, err := store.AdminByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("AdminByID: %v", err)
	}
	if byID.Username != "root-admin" {
		t.Errorf("AdminByID username = %q, want root-admin", byID.Username)
	}
	if byID.ID == 0 {
		t.Error("AdminByID returned an admin with id 0")
	}

	byUsername, err := store.AdminByUsername(ctx, "ROOT-ADMIN")
	if err != nil {
		t.Fatalf("AdminByUsername (case-insensitive): %v", err)
	}
	if byUsername.ID != a.ID {
		t.Errorf("AdminByUsername resolved a different account")
	}
}

func TestAdminByUsernameNotFound(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	if _, err := store.AdminByUsername(context.Background(), "nobody"); err == nil {
		t.Error("AdminByUsername(missing) = nil error, want not-found error")
	}
}

func TestAdminByTokenHash(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	a := &Admin{
		Username:     "tokenadmin",
		Email:        "tokenadmin@example.com",
		PasswordHash: "argon2id$fake-hash-for-testing",
		TokenHash:    "deadbeefdeadbeef",
		TokenPrefix:  "adm_dead",
	}
	if _, err := store.CreateAdmin(ctx, a); err != nil {
		t.Fatalf("CreateAdmin: %v", err)
	}

	found, err := store.AdminByTokenHash(ctx, "deadbeefdeadbeef")
	if err != nil {
		t.Fatalf("AdminByTokenHash: %v", err)
	}
	if found.ID != a.ID {
		t.Errorf("AdminByTokenHash resolved id %d, want %d", found.ID, a.ID)
	}

	if _, err := store.AdminByTokenHash(ctx, "not-a-real-hash"); err == nil {
		t.Error("AdminByTokenHash(unknown hash) = nil error, want not-found error")
	}
}

func TestCountAdmins(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	n, err := store.CountAdmins(ctx)
	if err != nil {
		t.Fatalf("CountAdmins (empty): %v", err)
	}
	if n != 0 {
		t.Errorf("CountAdmins (empty) = %d, want 0", n)
	}

	newTestAdmin(t, store, "adminone", "adminone@example.com")
	newTestAdmin(t, store, "admintwo", "admintwo@example.com")

	n, err = store.CountAdmins(ctx)
	if err != nil {
		t.Fatalf("CountAdmins: %v", err)
	}
	if n != 2 {
		t.Errorf("CountAdmins = %d, want 2", n)
	}
}

func TestSetAdminPasswordAndTOTP(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	a := newTestAdmin(t, store, "secadmin", "secadmin@example.com")

	if err := store.SetAdminPassword(ctx, a.ID, "argon2id$new-hash"); err != nil {
		t.Fatalf("SetAdminPassword: %v", err)
	}
	reloaded, err := store.AdminByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("AdminByID: %v", err)
	}
	if reloaded.PasswordHash != "argon2id$new-hash" {
		t.Errorf("password hash not updated, got %q", reloaded.PasswordHash)
	}

	if err := store.SetAdminTOTP(ctx, a.ID, "SECRET456", true); err != nil {
		t.Fatalf("SetAdminTOTP: %v", err)
	}
	reloaded, err = store.AdminByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("AdminByID: %v", err)
	}
	if reloaded.TOTPSecret != "SECRET456" || !reloaded.TOTPEnabled {
		t.Errorf("totp not updated correctly: secret=%q enabled=%v", reloaded.TOTPSecret, reloaded.TOTPEnabled)
	}
}

func TestRecordAdminLoginSuccessResetsFailuresAndLock(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	a := newTestAdmin(t, store, "loginadmin", "loginadmin@example.com")

	for i := 0; i < 3; i++ {
		if err := store.RecordAdminLoginFailure(ctx, a.ID); err != nil {
			t.Fatalf("RecordAdminLoginFailure: %v", err)
		}
	}
	reloaded, err := store.AdminByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("AdminByID: %v", err)
	}
	if reloaded.FailedLogins != 3 {
		t.Errorf("FailedLogins = %d, want 3", reloaded.FailedLogins)
	}

	if err := store.RecordAdminLoginSuccess(ctx, a.ID); err != nil {
		t.Fatalf("RecordAdminLoginSuccess: %v", err)
	}
	reloaded, err = store.AdminByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("AdminByID: %v", err)
	}
	if reloaded.FailedLogins != 0 || reloaded.LockedUntil != 0 {
		t.Errorf("login success did not clear counters: failed=%d locked=%d", reloaded.FailedLogins, reloaded.LockedUntil)
	}
}

func TestRecordAdminLoginFailureLocksAfterThreshold(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	a := newTestAdmin(t, store, "lockadmin", "lockadmin@example.com")

	for i := int64(0); i < LockoutThreshold; i++ {
		if err := store.RecordAdminLoginFailure(ctx, a.ID); err != nil {
			t.Fatalf("RecordAdminLoginFailure #%d: %v", i, err)
		}
	}
	reloaded, err := store.AdminByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("AdminByID: %v", err)
	}
	if reloaded.LockedUntil == 0 {
		t.Error("admin account not locked after crossing LockoutThreshold failures")
	}
	if reloaded.FailedLogins != 0 {
		t.Errorf("FailedLogins should reset to 0 once locked, got %d", reloaded.FailedLogins)
	}
	if !reloaded.Locked() {
		t.Error("Admin.Locked() = false for an account past LockoutThreshold")
	}
}

func TestAdminSessionLifecycle(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	a := newTestAdmin(t, store, "sessadmin", "sessadmin@example.com")

	sess := &Session{
		UserID:    a.ID,
		TokenHash: "sesshash123",
		ExpiresAt: 9999999999,
	}
	id, err := store.CreateAdminSession(ctx, sess)
	if err != nil {
		t.Fatalf("CreateAdminSession: %v", err)
	}
	if id == 0 {
		t.Fatal("CreateAdminSession returned id 0")
	}
	if sess.ID == 0 {
		t.Fatal("CreateAdminSession left Session.ID at 0")
	}

	found, err := store.AdminSessionByHash(ctx, "sesshash123")
	if err != nil {
		t.Fatalf("AdminSessionByHash: %v", err)
	}
	if found.UserID != a.ID {
		t.Errorf("AdminSessionByHash admin id = %d, want %d", found.UserID, a.ID)
	}

	if err := store.DeleteAdminSessionByHash(ctx, "sesshash123"); err != nil {
		t.Fatalf("DeleteAdminSessionByHash: %v", err)
	}
	if _, err := store.AdminSessionByHash(ctx, "sesshash123"); err == nil {
		t.Error("AdminSessionByHash succeeded after DeleteAdminSessionByHash, want not-found")
	}

	// Idempotency: deleting an already-deleted session must not error.
	if err := store.DeleteAdminSessionByHash(ctx, "sesshash123"); err != nil {
		t.Errorf("DeleteAdminSessionByHash (already deleted): %v", err)
	}
}

func TestDeleteAdminSessions(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	a := newTestAdmin(t, store, "multisess", "multisess@example.com")

	for i := 0; i < 3; i++ {
		sess := &Session{UserID: a.ID, TokenHash: "hash" + string(rune('a'+i)), ExpiresAt: 9999999999}
		if _, err := store.CreateAdminSession(ctx, sess); err != nil {
			t.Fatalf("CreateAdminSession #%d: %v", i, err)
		}
	}
	if err := store.DeleteAdminSessions(ctx, a.ID); err != nil {
		t.Fatalf("DeleteAdminSessions: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := store.AdminSessionByHash(ctx, "hash"+string(rune('a'+i))); err == nil {
			t.Errorf("session %d still resolves after DeleteAdminSessions", i)
		}
	}
}

func TestSetupTokenLifecycle(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()

	if err := store.CreateSetupToken(ctx, "setuphash1", "primary_admin", 9999999999); err != nil {
		t.Fatalf("CreateSetupToken: %v", err)
	}

	id, err := store.SetupTokenByHash(ctx, "setuphash1", "primary_admin")
	if err != nil {
		t.Fatalf("SetupTokenByHash: %v", err)
	}
	if id == 0 {
		t.Fatal("SetupTokenByHash returned id 0 for a freshly created token")
	}

	// Wrong purpose must not match.
	if _, err := store.SetupTokenByHash(ctx, "setuphash1", "other_purpose"); err == nil {
		t.Error("SetupTokenByHash(wrong purpose) succeeded, want not-found")
	}

	if err := store.ConsumeSetupToken(ctx, id); err != nil {
		t.Fatalf("ConsumeSetupToken: %v", err)
	}

	// A consumed token can no longer be resolved for redemption.
	if _, err := store.SetupTokenByHash(ctx, "setuphash1", "primary_admin"); err == nil {
		t.Error("SetupTokenByHash succeeded after ConsumeSetupToken, want not-found")
	}

	// Idempotency: consuming an already-consumed token must not error, and must not
	// resurrect it (the WHERE used = 0 guard makes the second call a no-op UPDATE).
	if err := store.ConsumeSetupToken(ctx, id); err != nil {
		t.Errorf("ConsumeSetupToken (already consumed): %v", err)
	}
}

func TestSetupTokenExpired(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	if err := store.CreateSetupToken(ctx, "expiredhash", "primary_admin", 1); err != nil {
		t.Fatalf("CreateSetupToken: %v", err)
	}
	if _, err := store.SetupTokenByHash(ctx, "expiredhash", "primary_admin"); err != database.ErrNotFound {
		t.Errorf("SetupTokenByHash(expired) err = %v, want database.ErrNotFound", err)
	}
}

func TestSetupTokenNeverExpires(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	// expires_at = 0 means "never expires" per the store's own check.
	if err := store.CreateSetupToken(ctx, "foreverhash", "primary_admin", 0); err != nil {
		t.Fatalf("CreateSetupToken: %v", err)
	}
	if _, err := store.SetupTokenByHash(ctx, "foreverhash", "primary_admin"); err != nil {
		t.Errorf("SetupTokenByHash(never-expiring): %v", err)
	}
}
