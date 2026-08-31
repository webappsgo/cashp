package auth

import (
	"context"
	"testing"
	"time"
)

func TestBootstrapNeededAndCompleteBootstrapFlow(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()

	needed, err := svc.BootstrapNeeded(ctx)
	if err != nil {
		t.Fatalf("BootstrapNeeded: %v", err)
	}
	if !needed {
		t.Fatal("BootstrapNeeded = false before any admin exists, want true")
	}

	token, err := svc.IssueSetupToken(ctx)
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}
	if token == "" {
		t.Fatal("IssueSetupToken returned an empty plaintext")
	}

	admin, session, aerr := svc.CompleteBootstrap(ctx, BootstrapInput{
		SetupToken: token,
		Username:   "primaryadmin",
		Email:      "primaryadmin@example.com",
		Password:   "a-good-password",
		IP:         "127.0.0.1",
	})
	if aerr != nil {
		t.Fatalf("CompleteBootstrap: %v", aerr)
	}
	if admin.ID == 0 {
		t.Fatal("CompleteBootstrap left Admin.ID at 0 — id regression")
	}
	if !admin.IsPrimary {
		t.Error("CompleteBootstrap did not mark the admin primary")
	}
	if session == "" {
		t.Fatal("CompleteBootstrap did not return a session token")
	}

	needed, err = svc.BootstrapNeeded(ctx)
	if err != nil {
		t.Fatalf("BootstrapNeeded (after): %v", err)
	}
	if needed {
		t.Error("BootstrapNeeded = true after an admin was created, want false")
	}

	// The setup token is single-use: a second bootstrap attempt with the same
	// (now-consumed) token, or any attempt once an admin exists, must fail.
	if _, _, aerr := svc.CompleteBootstrap(ctx, BootstrapInput{
		SetupToken: token,
		Username:   "seconduser",
		Email:      "seconduser@example.com",
		Password:   "a-good-password",
		IP:         "127.0.0.1",
	}); aerr == nil {
		t.Error("CompleteBootstrap succeeded a second time, want failure — admin already exists")
	}
}

func TestCompleteBootstrapRejectsUnknownToken(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	if _, _, aerr := svc.CompleteBootstrap(context.Background(), BootstrapInput{
		SetupToken: "not-a-real-token",
		Username:   "someone",
		Email:      "someone@example.com",
		Password:   "a-good-password",
		IP:         "127.0.0.1",
	}); aerr == nil {
		t.Error("CompleteBootstrap succeeded with an unknown token, want failure")
	}
}

func TestCompleteBootstrapRejectsInvalidFields(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	token, err := svc.IssueSetupToken(ctx)
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}
	if _, _, aerr := svc.CompleteBootstrap(ctx, BootstrapInput{
		SetupToken: token,
		Username:   "a",
		Email:      "primaryadmin@example.com",
		Password:   "a-good-password",
		IP:         "127.0.0.1",
	}); aerr == nil {
		t.Error("CompleteBootstrap succeeded with an invalid username, want failure")
	}
}

func TestAdminLoginSessionAndLogoutFlow(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	token, err := svc.IssueSetupToken(ctx)
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}
	admin, _, aerr := svc.CompleteBootstrap(ctx, BootstrapInput{
		SetupToken: token, Username: "loginadmin", Email: "loginadmin@example.com",
		Password: "a-good-password", IP: "127.0.0.1",
	})
	if aerr != nil {
		t.Fatalf("CompleteBootstrap: %v", aerr)
	}

	// Unauthenticated: an empty or bogus session token is rejected.
	if _, _, aerr := svc.ResolveAdminSession(ctx, ""); aerr == nil {
		t.Error("ResolveAdminSession(empty) succeeded, want failure")
	}
	if _, _, aerr := svc.ResolveAdminSession(ctx, "bogus-token"); aerr == nil {
		t.Error("ResolveAdminSession(bogus) succeeded, want failure")
	}

	// Invalid credentials are rejected.
	if _, _, aerr := svc.AdminLogin(ctx, LoginInput{
		Identifier: "loginadmin", Password: "wrong-password", IP: "127.0.0.1",
	}); aerr == nil {
		t.Error("AdminLogin(wrong password) succeeded, want failure")
	}

	// Valid credentials issue a working session.
	loggedIn, session, aerr := svc.AdminLogin(ctx, LoginInput{
		Identifier: "loginadmin", Password: "a-good-password", IP: "127.0.0.1",
	})
	if aerr != nil {
		t.Fatalf("AdminLogin: %v", aerr)
	}
	if loggedIn.ID != admin.ID {
		t.Errorf("AdminLogin resolved id = %d, want %d", loggedIn.ID, admin.ID)
	}

	resolved, _, aerr := svc.ResolveAdminSession(ctx, session)
	if aerr != nil {
		t.Fatalf("ResolveAdminSession: %v", aerr)
	}
	if resolved.ID != admin.ID {
		t.Errorf("ResolveAdminSession id = %d, want %d", resolved.ID, admin.ID)
	}

	svc.AdminLogout(ctx, session)
	if _, _, aerr := svc.ResolveAdminSession(ctx, session); aerr == nil {
		t.Error("ResolveAdminSession succeeded after AdminLogout, want the session revoked")
	}

	// Idempotency: logging out an already-revoked session must not panic or error visibly.
	svc.AdminLogout(ctx, session)
}

func TestAdminLoginLocksAfterThreshold(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	token, err := svc.IssueSetupToken(ctx)
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}
	if _, _, aerr := svc.CompleteBootstrap(ctx, BootstrapInput{
		SetupToken: token, Username: "lockadmin", Email: "lockadmin@example.com",
		Password: "a-good-password", IP: "127.0.0.1",
	}); aerr != nil {
		t.Fatalf("CompleteBootstrap: %v", aerr)
	}

	for i := int64(0); i < LockoutThreshold; i++ {
		if _, _, aerr := svc.AdminLogin(ctx, LoginInput{
			Identifier: "lockadmin", Password: "wrong-password", IP: "127.0.0.1",
		}); aerr == nil {
			t.Fatal("AdminLogin(wrong password) succeeded, want failure")
		}
	}
	if _, _, aerr := svc.AdminLogin(ctx, LoginInput{
		Identifier: "lockadmin", Password: "a-good-password", IP: "127.0.0.1",
	}); aerr == nil {
		t.Error("AdminLogin succeeded for a locked account with the correct password, want failure")
	}
}

func TestCreateAdminRejectsDuplicateUsername(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	first, aerr := svc.CreateAdmin(ctx, 0, "seconadmin", "seconadmin@example.com", "a-good-password")
	if aerr != nil {
		t.Fatalf("CreateAdmin: %v", aerr)
	}
	if first.ID == 0 {
		t.Fatal("CreateAdmin left Admin.ID at 0 — id regression")
	}
	if _, aerr := svc.CreateAdmin(ctx, first.ID, "seconadmin", "other@example.com", "a-good-password"); aerr == nil {
		t.Error("CreateAdmin succeeded with a taken username, want failure")
	}
}

func TestCreateAdminRejectsInvalidFields(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	cases := []struct {
		name, username, email, password string
	}{
		{"bad username", "a", "ok@example.com", "a-good-password"},
		{"bad email", "gooduser", "not-an-email", "a-good-password"},
		{"bad password", "gooduser", "ok@example.com", "short"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, aerr := svc.CreateAdmin(ctx, 0, c.username, c.email, c.password); aerr == nil {
				t.Error("CreateAdmin succeeded with invalid input, want validation error")
			}
		})
	}
}

func TestChangeAdminPasswordRevokesOtherSessions(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	token, err := svc.IssueSetupToken(ctx)
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}
	admin, firstSession, aerr := svc.CompleteBootstrap(ctx, BootstrapInput{
		SetupToken: token, Username: "changeadmin", Email: "changeadmin@example.com",
		Password: "a-good-password", IP: "127.0.0.1",
	})
	if aerr != nil {
		t.Fatalf("CompleteBootstrap: %v", aerr)
	}

	if _, aerr := svc.ChangeAdminPassword(ctx, admin.ID, "wrong-current", "a-new-good-password", "127.0.0.1", ""); aerr == nil {
		t.Error("ChangeAdminPassword succeeded with the wrong current password, want failure")
	}

	newSession, aerr := svc.ChangeAdminPassword(ctx, admin.ID, "a-good-password", "a-new-good-password", "127.0.0.1", "")
	if aerr != nil {
		t.Fatalf("ChangeAdminPassword: %v", aerr)
	}
	if newSession == "" {
		t.Fatal("ChangeAdminPassword did not return a new session token")
	}
	if _, _, aerr := svc.ResolveAdminSession(ctx, firstSession); aerr == nil {
		t.Error("the pre-change session still resolves after ChangeAdminPassword, want it revoked")
	}
	if _, _, aerr := svc.ResolveAdminSession(ctx, newSession); aerr != nil {
		t.Errorf("ResolveAdminSession(new session): %v", aerr)
	}
}

func TestAdminTOTPLifecycle(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	token, err := svc.IssueSetupToken(ctx)
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}
	admin, _, aerr := svc.CompleteBootstrap(ctx, BootstrapInput{
		SetupToken: token, Username: "totpadmin", Email: "totpadmin@example.com",
		Password: "a-good-password", IP: "127.0.0.1",
	})
	if aerr != nil {
		t.Fatalf("CompleteBootstrap: %v", aerr)
	}

	secret, uri, aerr := svc.BeginAdminTOTP(ctx, admin.ID)
	if aerr != nil {
		t.Fatalf("BeginAdminTOTP: %v", aerr)
	}
	if secret == "" || uri == "" {
		t.Fatal("BeginAdminTOTP returned an empty secret or URI")
	}

	if aerr := svc.ConfirmAdminTOTP(ctx, admin.ID, "000000", "127.0.0.1"); aerr == nil {
		code, _ := TOTPCode(secret, time.Now())
		if code == "000000" {
			t.Skip("random secret happened to produce 000000 for the current window")
		}
		t.Error("ConfirmAdminTOTP succeeded with a wrong code, want failure")
	}

	code, err := TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if aerr := svc.ConfirmAdminTOTP(ctx, admin.ID, code, "127.0.0.1"); aerr != nil {
		t.Fatalf("ConfirmAdminTOTP: %v", aerr)
	}

	// Disabling requires both the password and a current code.
	if aerr := svc.DisableAdminTOTP(ctx, admin.ID, "wrong-password", code); aerr == nil {
		t.Error("DisableAdminTOTP succeeded with the wrong password, want failure")
	}
	code2, err := TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if aerr := svc.DisableAdminTOTP(ctx, admin.ID, "a-good-password", code2); aerr != nil {
		t.Fatalf("DisableAdminTOTP: %v", aerr)
	}
}

func TestSetUserApprovalAndSuspendOrg(t *testing.T) {
	svc := newTestServiceWithConfig(t, nil)
	ctx := context.Background()
	u, _, aerr := svc.Register(ctx, RegisterInput{
		Username: "approveme", Email: "approveme@example.com", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Register: %v", aerr)
	}
	if aerr := svc.SetUserApproval(ctx, 1, u.ID, true, false); aerr != nil {
		t.Errorf("SetUserApproval: %v", aerr)
	}

	org, aerr := svc.CreateOrg(ctx, u.ID, OrgInput{Slug: "suspendorg", Name: "Suspend Org"}, "")
	if aerr != nil {
		t.Fatalf("CreateOrg: %v", aerr)
	}
	if aerr := svc.SuspendOrg(ctx, 1, org.ID, true); aerr != nil {
		t.Errorf("SuspendOrg(true): %v", aerr)
	}
	// Idempotency: suspending again, and restoring, must not error.
	if aerr := svc.SuspendOrg(ctx, 1, org.ID, true); aerr != nil {
		t.Errorf("SuspendOrg(true again): %v", aerr)
	}
	if aerr := svc.SuspendOrg(ctx, 1, org.ID, false); aerr != nil {
		t.Errorf("SuspendOrg(false): %v", aerr)
	}
}
