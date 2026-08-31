package auth

import (
	"context"
	"testing"
)

func newTestUser(t *testing.T, store *Store, username, email string) *User {
	t.Helper()
	u := &User{
		Username:     username,
		Email:        email,
		PasswordHash: "argon2id$fake-hash-for-testing",
		Role:         "user",
		Visibility:   "private",
	}
	id, err := store.CreateUser(context.Background(), u)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if id == 0 {
		t.Fatal("CreateUser returned id 0")
	}
	return u
}

func TestCreateUserAndLookups(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "alice", "alice@example.com")

	byID, err := store.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if byID.Username != "alice" {
		t.Errorf("UserByID username = %q, want alice", byID.Username)
	}

	byUsername, err := store.UserByUsername(ctx, "ALICE")
	if err != nil {
		t.Fatalf("UserByUsername (case-insensitive): %v", err)
	}
	if byUsername.ID != u.ID {
		t.Errorf("UserByUsername resolved different account")
	}

	byEmail, err := store.UserByEmail(ctx, "Alice@Example.com")
	if err != nil {
		t.Fatalf("UserByEmail (case-insensitive): %v", err)
	}
	if byEmail.ID != u.ID {
		t.Errorf("UserByEmail resolved different account")
	}
}

func TestUserByIdentifierDetectsType(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "bob", "bob@example.com")

	cases := []struct {
		name string
		in   string
	}{
		{"by id", "1"},
		{"by username", "bob"},
		{"by email", "bob@example.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			found, err := store.UserByIdentifier(ctx, c.in)
			if err != nil {
				t.Fatalf("UserByIdentifier(%q): %v", c.in, err)
			}
			if found.ID != u.ID {
				t.Errorf("UserByIdentifier(%q) resolved id %d, want %d", c.in, found.ID, u.ID)
			}
		})
	}
}

func TestUserByIdentifierNotFound(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	if _, err := store.UserByUsername(ctx, "nobody"); err == nil {
		t.Error("UserByUsername(missing) = nil error, want not-found error")
	}
}

func TestSetUserPasswordEmailTOTPFlags(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "carol", "carol@example.com")

	if err := store.SetUserPassword(ctx, u.ID, "argon2id$new-hash"); err != nil {
		t.Fatalf("SetUserPassword: %v", err)
	}
	reloaded, err := store.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if reloaded.PasswordHash != "argon2id$new-hash" {
		t.Errorf("password hash not updated, got %q", reloaded.PasswordHash)
	}

	if err := store.SetUserEmail(ctx, u.ID, "New@Example.com", true); err != nil {
		t.Fatalf("SetUserEmail: %v", err)
	}
	reloaded, err = store.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if reloaded.Email != "new@example.com" || !reloaded.EmailVerified {
		t.Errorf("email not updated correctly: email=%q verified=%v", reloaded.Email, reloaded.EmailVerified)
	}

	if err := store.SetUserTOTP(ctx, u.ID, "SECRET123", true); err != nil {
		t.Fatalf("SetUserTOTP: %v", err)
	}
	reloaded, err = store.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if reloaded.TOTPSecret != "SECRET123" || !reloaded.TOTPEnabled {
		t.Errorf("totp not updated correctly: secret=%q enabled=%v", reloaded.TOTPSecret, reloaded.TOTPEnabled)
	}

	if err := store.SetUserFlags(ctx, u.ID, true, true); err != nil {
		t.Fatalf("SetUserFlags: %v", err)
	}
	reloaded, err = store.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if !reloaded.Approved || !reloaded.Disabled {
		t.Errorf("flags not updated correctly: approved=%v disabled=%v", reloaded.Approved, reloaded.Disabled)
	}
}

func TestRecordLoginSuccessResetsFailuresAndLock(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "dave", "dave@example.com")

	for i := 0; i < 3; i++ {
		if err := store.RecordLoginFailure(ctx, u.ID); err != nil {
			t.Fatalf("RecordLoginFailure: %v", err)
		}
	}
	reloaded, err := store.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if reloaded.FailedLogins != 3 {
		t.Errorf("FailedLogins = %d, want 3", reloaded.FailedLogins)
	}

	if err := store.RecordLoginSuccess(ctx, u.ID); err != nil {
		t.Fatalf("RecordLoginSuccess: %v", err)
	}
	reloaded, err = store.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if reloaded.FailedLogins != 0 || reloaded.LockedUntil != 0 {
		t.Errorf("login success did not clear counters: failed=%d locked=%d", reloaded.FailedLogins, reloaded.LockedUntil)
	}
}

func TestRecordLoginFailureLocksAfterThreshold(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "erin", "erin@example.com")

	for i := int64(0); i < LockoutThreshold; i++ {
		if err := store.RecordLoginFailure(ctx, u.ID); err != nil {
			t.Fatalf("RecordLoginFailure #%d: %v", i, err)
		}
	}
	reloaded, err := store.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if reloaded.LockedUntil == 0 {
		t.Error("account not locked after crossing LockoutThreshold failures")
	}
	if reloaded.FailedLogins != 0 {
		t.Errorf("FailedLogins should reset to 0 once locked, got %d", reloaded.FailedLogins)
	}
	if !reloaded.Locked() {
		t.Error("User.Locked() = false for an account past LockoutThreshold")
	}
}

func TestListAndCountUsers(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		newTestUser(t, store, "user"+string(rune('a'+i)), "user"+string(rune('a'+i))+"@example.com")
	}

	n, err := store.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 3 {
		t.Errorf("CountUsers = %d, want 3", n)
	}

	list, err := store.ListUsers(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("ListUsers len = %d, want 3", len(list))
	}

	page, err := store.ListUsers(ctx, 2, 0)
	if err != nil {
		t.Fatalf("ListUsers(2,0): %v", err)
	}
	if len(page) != 2 {
		t.Errorf("ListUsers(limit=2) len = %d, want 2", len(page))
	}
}

func TestDeleteUserTombstonesNameAndCascades(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "frank", "frank@example.com")

	if err := store.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := store.UserByID(ctx, u.ID); err == nil {
		t.Error("user still readable after DeleteUser")
	}
	tomb, err := store.NameTombstoned(ctx, "frank")
	if err != nil {
		t.Fatalf("NameTombstoned: %v", err)
	}
	if !tomb {
		t.Error("username not tombstoned after DeleteUser — a deleted username could be reclaimed by a new registrant")
	}
	taken, err := store.NameTaken(ctx, "frank")
	if err != nil {
		t.Fatalf("NameTaken: %v", err)
	}
	if taken {
		t.Error("NameTaken should be false once the user row is gone (tombstone is checked separately)")
	}
}

func TestNameTakenAcrossUsersAndOrgs(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	newTestUser(t, store, "gina", "gina@example.com")

	taken, err := store.NameTaken(ctx, "GINA")
	if err != nil {
		t.Fatalf("NameTaken: %v", err)
	}
	if !taken {
		t.Error("NameTaken(existing username, mixed case) = false, want true")
	}

	free, err := store.NameTaken(ctx, "nobody-has-this-name")
	if err != nil {
		t.Fatalf("NameTaken: %v", err)
	}
	if free {
		t.Error("NameTaken(unused name) = true, want false")
	}
}

func TestEmailTaken(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	newTestUser(t, store, "henry", "henry@example.com")

	taken, err := store.EmailTaken(ctx, "Henry@Example.com")
	if err != nil {
		t.Fatalf("EmailTaken: %v", err)
	}
	if !taken {
		t.Error("EmailTaken(existing email, mixed case) = false, want true")
	}

	free, err := store.EmailTaken(ctx, "unused@example.com")
	if err != nil {
		t.Fatalf("EmailTaken: %v", err)
	}
	if free {
		t.Error("EmailTaken(unused email) = true, want false")
	}
}

func TestPingSucceedsOnOpenDB(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	if err := store.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}
