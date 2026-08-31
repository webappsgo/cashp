package auth

import (
	"context"
	"testing"
	"time"
)

// newTestServiceWithConfig builds a real Service against a real sqlite test DB,
// using a config with email verification off so registration issues a
// usable session immediately (verification itself is exercised separately).
func newTestServiceWithConfig(t *testing.T, mutate func(*Config)) *Service {
	t.Helper()
	db := newAuthTestDB(t)
	cfg := DefaultConfig()
	cfg.RequireEmailVerification = false
	if mutate != nil {
		mutate(&cfg)
	}
	svc, err := New(Options{Store: NewStore(db), Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

// TestRegisterLoginSessionFlow exercises the full real auth flow required by
// testing-rules.md: unauthenticated rejected -> register (create account) ->
// login -> session-based access -> invalid credentials rejected.
func TestRegisterLoginSessionFlow(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()

	// Unauthenticated: an empty session token must be rejected.
	if _, _, aerr := svc.ResolveSession(ctx, ""); aerr == nil {
		t.Fatal("ResolveSession(empty token) succeeded, want ErrUnauthenticated")
	}
	if _, _, aerr := svc.ResolveSession(ctx, "not-a-real-token"); aerr == nil {
		t.Fatal("ResolveSession(bogus token) succeeded, want ErrUnauthenticated")
	}

	// Register a new account.
	u, regToken, aerr := svc.Register(ctx, RegisterInput{
		Username: "newuser",
		Email:    "newuser@example.com",
		Password: "a-good-password",
		IP:       "127.0.0.1",
	})
	if aerr != nil {
		t.Fatalf("Register: %v", aerr)
	}
	if u.Username != "newuser" {
		t.Errorf("registered username = %q, want newuser", u.Username)
	}
	if regToken == "" {
		t.Fatal("Register did not return a session token")
	}

	// The registration session token itself must resolve to the new account.
	resolved, _, aerr := svc.ResolveSession(ctx, regToken)
	if aerr != nil {
		t.Fatalf("ResolveSession(registration token): %v", aerr)
	}
	if resolved.ID != u.ID {
		t.Errorf("resolved session user id = %d, want %d", resolved.ID, u.ID)
	}

	// Login with valid credentials issues a working session.
	loggedIn, loginToken, aerr := svc.Login(ctx, LoginInput{
		Identifier: "newuser",
		Password:   "a-good-password",
		IP:         "127.0.0.1",
	})
	if aerr != nil {
		t.Fatalf("Login(valid credentials): %v", aerr)
	}
	if loggedIn.ID != u.ID {
		t.Errorf("login resolved user id = %d, want %d", loggedIn.ID, u.ID)
	}
	if loginToken == "" {
		t.Fatal("Login did not return a session token")
	}

	// Session-based access: the login token resolves back to the same account.
	viaSession, _, aerr := svc.ResolveSession(ctx, loginToken)
	if aerr != nil {
		t.Fatalf("ResolveSession(login token): %v", aerr)
	}
	if viaSession.ID != u.ID {
		t.Errorf("session-resolved user id = %d, want %d", viaSession.ID, u.ID)
	}

	// Invalid credentials (wrong password) are rejected.
	if _, _, aerr := svc.Login(ctx, LoginInput{
		Identifier: "newuser",
		Password:   "totally-wrong-password",
		IP:         "127.0.0.1",
	}); aerr == nil {
		t.Error("Login(wrong password) succeeded, want failure")
	}

	// Invalid credentials (unknown account) are rejected with the same
	// generic error, never a distinguishing "no such user" message.
	_, _, unknownErr := svc.Login(ctx, LoginInput{
		Identifier: "no-such-user",
		Password:   "whatever",
		IP:         "127.0.0.1",
	})
	if unknownErr == nil {
		t.Fatal("Login(unknown identifier) succeeded, want failure")
	}
	_, _, wrongPassErr := svc.Login(ctx, LoginInput{
		Identifier: "newuser",
		Password:   "totally-wrong-password",
		IP:         "127.0.0.1",
	})
	if wrongPassErr == nil || wrongPassErr.Code != unknownErr.Code || wrongPassErr.Message != unknownErr.Message {
		t.Errorf("unknown-account and wrong-password errors differ (code=%v/%v msg=%q/%q); login must not let a caller distinguish them",
			unknownErr.Code, wrongPassErr.Code, unknownErr.Message, wrongPassErr.Message)
	}

	// Logout revokes the session.
	svc.Logout(ctx, loginToken)
	if _, _, aerr := svc.ResolveSession(ctx, loginToken); aerr == nil {
		t.Error("ResolveSession succeeded after Logout, want the revoked session to be rejected")
	}
}

func TestRegisterRejectsWhenUsersDisabled(t *testing.T) {
	svc := newTestServiceWithConfig(t, func(c *Config) { c.UsersEnabled = false })
	_, _, aerr := svc.Register(context.Background(), RegisterInput{
		Username: "someone",
		Email:    "someone@example.com",
		Password: "a-good-password",
	})
	if aerr == nil {
		t.Fatal("Register succeeded with UsersEnabled=false, want ErrFeatureDisabled")
	}
}

func TestRegisterRejectsDuplicateUsernameAndEmail(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	if _, _, aerr := svc.Register(ctx, RegisterInput{
		Username: "dupe", Email: "dupe@example.com", Password: "a-good-password",
	}); aerr != nil {
		t.Fatalf("first Register: %v", aerr)
	}

	if _, _, aerr := svc.Register(ctx, RegisterInput{
		Username: "dupe", Email: "different@example.com", Password: "a-good-password",
	}); aerr == nil {
		t.Error("Register with a taken username succeeded, want failure")
	}

	if _, _, aerr := svc.Register(ctx, RegisterInput{
		Username: "someone-else", Email: "dupe@example.com", Password: "a-good-password",
	}); aerr == nil {
		t.Error("Register with a taken email succeeded, want failure")
	}
}

func TestRegisterRejectsInvalidFields(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	cases := []struct {
		name string
		in   RegisterInput
	}{
		{"bad username", RegisterInput{Username: "a", Email: "ok@example.com", Password: "a-good-password"}},
		{"bad email", RegisterInput{Username: "gooduser", Email: "not-an-email", Password: "a-good-password"}},
		{"bad password", RegisterInput{Username: "gooduser", Email: "ok@example.com", Password: "short"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, aerr := svc.Register(ctx, c.in); aerr == nil {
				t.Error("Register succeeded with invalid input, want validation error")
			}
		})
	}
}

func TestRegisterClosedWhenRegistrationDisabled(t *testing.T) {
	svc := newTestServiceWithConfig(t, func(c *Config) { c.RegistrationMode = RegistrationDisabled })
	_, _, aerr := svc.Register(context.Background(), RegisterInput{
		Username: "someone", Email: "someone@example.com", Password: "a-good-password",
	})
	if aerr == nil {
		t.Fatal("Register succeeded with RegistrationDisabled, want failure")
	}
}

func TestLoginRejectsDisabledAndUnapprovedAccounts(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	u, _, aerr := svc.Register(ctx, RegisterInput{
		Username: "flagged", Email: "flagged@example.com", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Register: %v", aerr)
	}

	if err := svc.Store().SetUserFlags(ctx, u.ID, true, true); err != nil {
		t.Fatalf("SetUserFlags(disabled): %v", err)
	}
	if _, _, aerr := svc.Login(ctx, LoginInput{Identifier: "flagged", Password: "a-good-password"}); aerr == nil {
		t.Error("Login succeeded for a disabled account, want failure")
	}

	if err := svc.Store().SetUserFlags(ctx, u.ID, false, false); err != nil {
		t.Fatalf("SetUserFlags(unapproved): %v", err)
	}
	if _, _, aerr := svc.Login(ctx, LoginInput{Identifier: "flagged", Password: "a-good-password"}); aerr == nil {
		t.Error("Login succeeded for an unapproved account, want failure")
	}
}

func TestLoginRejectsLockedAccountEvenWithCorrectPassword(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	u, _, aerr := svc.Register(ctx, RegisterInput{
		Username: "lockme", Email: "lockme@example.com", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Register: %v", aerr)
	}
	for i := int64(0); i < LockoutThreshold; i++ {
		if err := svc.Store().RecordLoginFailure(ctx, u.ID); err != nil {
			t.Fatalf("RecordLoginFailure: %v", err)
		}
	}
	if _, _, aerr := svc.Login(ctx, LoginInput{Identifier: "lockme", Password: "a-good-password"}); aerr == nil {
		t.Error("Login succeeded for a locked account, want failure")
	}
}

func TestLoginRequiresTOTPWhenEnabled(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	u, _, aerr := svc.Register(ctx, RegisterInput{
		Username: "twofactor", Email: "twofactor@example.com", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Register: %v", aerr)
	}
	secret := newTestTOTPSecret(t)
	if err := svc.Store().SetUserTOTP(ctx, u.ID, secret, true); err != nil {
		t.Fatalf("SetUserTOTP: %v", err)
	}

	if _, _, aerr := svc.Login(ctx, LoginInput{Identifier: "twofactor", Password: "a-good-password"}); aerr == nil {
		t.Error("Login without a TOTP code succeeded on a 2FA-enabled account, want ErrTwoFactorRequired")
	}

	if _, _, aerr := svc.Login(ctx, LoginInput{
		Identifier: "twofactor", Password: "a-good-password", TOTPCode: "000000",
	}); aerr == nil {
		code, _ := TOTPCode(secret, time.Now())
		if code == "000000" {
			t.Skip("random secret happened to produce 000000 for the current window")
		}
		t.Error("Login with a wrong TOTP code succeeded, want ErrTwoFactorInvalid")
	}

	code, err := TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if _, _, aerr := svc.Login(ctx, LoginInput{
		Identifier: "twofactor", Password: "a-good-password", TOTPCode: code,
	}); aerr != nil {
		t.Errorf("Login with a valid TOTP code failed: %v", aerr)
	}
}

func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	u, firstToken, aerr := svc.Register(ctx, RegisterInput{
		Username: "changer", Email: "changer@example.com", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Register: %v", aerr)
	}

	if _, aerr := svc.ChangePassword(ctx, u.ID, "wrong-current-password", "a-new-good-password", "", ""); aerr == nil {
		t.Error("ChangePassword succeeded with the wrong current password, want failure")
	}

	newToken, aerr := svc.ChangePassword(ctx, u.ID, "a-good-password", "a-new-good-password", "127.0.0.1", "")
	if aerr != nil {
		t.Fatalf("ChangePassword: %v", aerr)
	}
	if newToken == "" {
		t.Fatal("ChangePassword did not return a new session token")
	}

	if _, _, aerr := svc.ResolveSession(ctx, firstToken); aerr == nil {
		t.Error("the pre-change session token still resolves after ChangePassword, want it revoked")
	}
	if _, _, aerr := svc.ResolveSession(ctx, newToken); aerr != nil {
		t.Errorf("ResolveSession(new token after ChangePassword): %v", aerr)
	}

	if _, _, aerr := svc.Login(ctx, LoginInput{Identifier: "changer", Password: "a-good-password"}); aerr == nil {
		t.Error("Login with the old password succeeded after ChangePassword, want failure")
	}
	if _, _, aerr := svc.Login(ctx, LoginInput{Identifier: "changer", Password: "a-new-good-password"}); aerr != nil {
		t.Errorf("Login with the new password failed: %v", aerr)
	}
}

func TestCheckNameReportsBlockedTakenAndAvailable(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	if _, _, aerr := svc.Register(ctx, RegisterInput{
		Username: "taken-name", Email: "takenname@example.com", Password: "a-good-password",
	}); aerr != nil {
		t.Fatalf("Register: %v", aerr)
	}

	if aerr := svc.CheckName(ctx, "taken-name"); aerr == nil {
		t.Error("CheckName(taken) = nil, want unavailable error")
	}
	if aerr := svc.CheckName(ctx, "admin"); aerr == nil {
		t.Error("CheckName(blocklisted) = nil, want reserved error")
	}
	if aerr := svc.CheckName(ctx, "brand-new-name"); aerr != nil {
		t.Errorf("CheckName(available) = %v, want nil", aerr)
	}
}
