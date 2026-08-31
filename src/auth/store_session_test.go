package auth

import (
	"context"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/database"
)

func TestCreateSessionAndLookup(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "sessionuser", "sessionuser@example.com")

	sess := &Session{
		UserID:    u.ID,
		TokenHash: "usersesshash",
		IPAddress: "127.0.0.1",
		UserAgent: "test-agent",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	id, err := store.CreateSession(ctx, sess)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if id == 0 {
		t.Fatal("CreateSession returned id 0")
	}
	if sess.ID == 0 {
		t.Fatal("CreateSession left Session.ID at 0")
	}

	found, err := store.SessionByHash(ctx, "usersesshash")
	if err != nil {
		t.Fatalf("SessionByHash: %v", err)
	}
	if found.UserID != u.ID {
		t.Errorf("SessionByHash user id = %d, want %d", found.UserID, u.ID)
	}
}

func TestSessionByHashNotFound(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	if _, err := store.SessionByHash(context.Background(), "no-such-hash"); err == nil {
		t.Error("SessionByHash(missing) = nil error, want not-found error")
	}
}

func TestListSessionsOrderedNewestFirst(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "listsessuser", "listsessuser@example.com")

	base := time.Now().Unix()
	for i, hash := range []string{"h1", "h2", "h3"} {
		sess := &Session{
			UserID:    u.ID,
			TokenHash: hash,
			ExpiresAt: base + 1000,
			CreatedAt: base + int64(i),
		}
		if _, err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession(%s): %v", hash, err)
		}
	}

	list, err := store.ListSessions(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("ListSessions len = %d, want 3", len(list))
	}
	if list[0].TokenHash != "h3" || list[2].TokenHash != "h1" {
		t.Errorf("ListSessions not newest-first: %v, %v, %v", list[0].TokenHash, list[1].TokenHash, list[2].TokenHash)
	}
}

func TestListSessionsEmpty(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "nosessuser", "nosessuser@example.com")

	list, err := store.ListSessions(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListSessions(empty): %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListSessions(empty) len = %d, want 0", len(list))
	}
}

func TestDeleteSessionByHash(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "delsessbyhash", "delsessbyhash@example.com")
	sess := &Session{UserID: u.ID, TokenHash: "delhash", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if _, err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := store.DeleteSessionByHash(ctx, "delhash"); err != nil {
		t.Fatalf("DeleteSessionByHash: %v", err)
	}
	if _, err := store.SessionByHash(ctx, "delhash"); err == nil {
		t.Error("session still resolves after DeleteSessionByHash")
	}
	// Idempotency: deleting an already-deleted session by hash must not error.
	if err := store.DeleteSessionByHash(ctx, "delhash"); err != nil {
		t.Errorf("DeleteSessionByHash (already deleted): %v", err)
	}
}

func TestDeleteSessionScopedByUser(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	owner := newTestUser(t, store, "sessscopeowner", "sessscopeowner@example.com")
	attacker := newTestUser(t, store, "sessscopeattacker", "sessscopeattacker@example.com")
	sess := &Session{UserID: owner.ID, TokenHash: "scopehash", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if _, err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Cross-user delete must be a no-op — it must not touch a session it doesn't own.
	if err := store.DeleteSession(ctx, attacker.ID, sess.ID); err != nil {
		t.Fatalf("DeleteSession(wrong user): %v", err)
	}
	if _, err := store.SessionByHash(ctx, "scopehash"); err != nil {
		t.Error("cross-user DeleteSession removed a session it did not own")
	}

	if err := store.DeleteSession(ctx, owner.ID, sess.ID); err != nil {
		t.Fatalf("DeleteSession(owner): %v", err)
	}
	if _, err := store.SessionByHash(ctx, "scopehash"); err == nil {
		t.Error("session still resolves after owner-scoped DeleteSession")
	}
}

func TestDeleteUserSessions(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "delallsess", "delallsess@example.com")
	for _, hash := range []string{"a1", "a2", "a3"} {
		sess := &Session{UserID: u.ID, TokenHash: hash, ExpiresAt: time.Now().Add(time.Hour).Unix()}
		if _, err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession(%s): %v", hash, err)
		}
	}
	if err := store.DeleteUserSessions(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUserSessions: %v", err)
	}
	list, err := store.ListSessions(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListSessions after DeleteUserSessions len = %d, want 0", len(list))
	}
}

func TestTrimUserSessionsKeepsNewest(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "trimsessuser", "trimsessuser@example.com")
	base := time.Now().Unix()
	for i, hash := range []string{"t1", "t2", "t3", "t4"} {
		sess := &Session{UserID: u.ID, TokenHash: hash, ExpiresAt: base + 1000, CreatedAt: base + int64(i)}
		if _, err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession(%s): %v", hash, err)
		}
	}

	if err := store.TrimUserSessions(ctx, u.ID, 2); err != nil {
		t.Fatalf("TrimUserSessions: %v", err)
	}
	list, err := store.ListSessions(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListSessions after trim len = %d, want 2", len(list))
	}
	if list[0].TokenHash != "t4" || list[1].TokenHash != "t3" {
		t.Errorf("TrimUserSessions kept the wrong sessions: %v, %v", list[0].TokenHash, list[1].TokenHash)
	}
}

func TestTrimUserSessionsNoopOnZeroOrNegativeKeep(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "trimzero", "trimzero@example.com")
	sess := &Session{UserID: u.ID, TokenHash: "zhash", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if _, err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.TrimUserSessions(ctx, u.ID, 0); err != nil {
		t.Fatalf("TrimUserSessions(keep=0): %v", err)
	}
	if err := store.TrimUserSessions(ctx, u.ID, -1); err != nil {
		t.Fatalf("TrimUserSessions(keep=-1): %v", err)
	}
	list, err := store.ListSessions(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("TrimUserSessions(keep<=0) should be a no-op, got len = %d", len(list))
	}
}

func TestPurgeExpiredSessionsUserAndAdmin(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "purgesessuser", "purgesessuser@example.com")
	a := newTestAdmin(t, store, "purgesessadmin", "purgesessadmin@example.com")

	past := time.Now().Add(-time.Hour).Unix()
	future := time.Now().Add(time.Hour).Unix()

	if _, err := store.CreateSession(ctx, &Session{UserID: u.ID, TokenHash: "expiredu", ExpiresAt: past}); err != nil {
		t.Fatalf("CreateSession(expired user): %v", err)
	}
	if _, err := store.CreateSession(ctx, &Session{UserID: u.ID, TokenHash: "liveu", ExpiresAt: future}); err != nil {
		t.Fatalf("CreateSession(live user): %v", err)
	}
	if _, err := store.CreateAdminSession(ctx, &Session{UserID: a.ID, TokenHash: "expireda", ExpiresAt: past}); err != nil {
		t.Fatalf("CreateAdminSession(expired admin): %v", err)
	}
	if _, err := store.CreateAdminSession(ctx, &Session{UserID: a.ID, TokenHash: "livea", ExpiresAt: future}); err != nil {
		t.Fatalf("CreateAdminSession(live admin): %v", err)
	}

	n, err := store.PurgeExpiredSessions(ctx)
	if err != nil {
		t.Fatalf("PurgeExpiredSessions: %v", err)
	}
	if n != 2 {
		t.Errorf("PurgeExpiredSessions purged %d rows, want 2 (one user + one admin)", n)
	}

	if _, err := store.SessionByHash(ctx, "liveu"); err != nil {
		t.Error("PurgeExpiredSessions removed a live user session")
	}
	if _, err := store.AdminSessionByHash(ctx, "livea"); err != nil {
		t.Error("PurgeExpiredSessions removed a live admin session")
	}
	if _, err := store.SessionByHash(ctx, "expiredu"); err == nil {
		t.Error("expired user session survived PurgeExpiredSessions")
	}

	// Idempotency: purging again with nothing left to purge must not error.
	n2, err := store.PurgeExpiredSessions(ctx)
	if err != nil {
		t.Fatalf("PurgeExpiredSessions (second run): %v", err)
	}
	if n2 != 0 {
		t.Errorf("PurgeExpiredSessions (second run) purged %d rows, want 0", n2)
	}
}

func TestPasswordResetLifecycle(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "pwresetuser", "pwresetuser@example.com")

	if err := store.CreatePasswordReset(ctx, u.ID, "resethash1", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatalf("CreatePasswordReset: %v", err)
	}

	userID, resetID, err := store.PasswordResetByHash(ctx, "resethash1")
	if err != nil {
		t.Fatalf("PasswordResetByHash: %v", err)
	}
	if userID != u.ID {
		t.Errorf("PasswordResetByHash user id = %d, want %d", userID, u.ID)
	}
	if resetID == 0 {
		t.Fatal("PasswordResetByHash returned a reset id of 0")
	}

	if err := store.ConsumePasswordReset(ctx, resetID); err != nil {
		t.Fatalf("ConsumePasswordReset: %v", err)
	}

	// A consumed reset token must not be usable again.
	if _, _, err := store.PasswordResetByHash(ctx, "resethash1"); err != database.ErrNotFound {
		t.Errorf("PasswordResetByHash(consumed) err = %v, want database.ErrNotFound", err)
	}

	// Idempotency: consuming twice must not error.
	if err := store.ConsumePasswordReset(ctx, resetID); err != nil {
		t.Errorf("ConsumePasswordReset (already consumed): %v", err)
	}
}

func TestPasswordResetByHashExpired(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "pwresetexp", "pwresetexp@example.com")
	if err := store.CreatePasswordReset(ctx, u.ID, "expiredresethash", 1); err != nil {
		t.Fatalf("CreatePasswordReset: %v", err)
	}
	if _, _, err := store.PasswordResetByHash(ctx, "expiredresethash"); err != database.ErrNotFound {
		t.Errorf("PasswordResetByHash(expired) err = %v, want database.ErrNotFound", err)
	}
}

func TestPasswordResetByHashUnknown(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	if _, _, err := store.PasswordResetByHash(context.Background(), "unknown-hash"); err == nil {
		t.Error("PasswordResetByHash(unknown) = nil error, want an error")
	}
}

func TestEmailVerificationLifecycle(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "emailverifuser", "emailverifuser@example.com")

	if err := store.CreateEmailVerification(ctx, u.ID, "New@Example.com", "verifyhash1", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatalf("CreateEmailVerification: %v", err)
	}

	userID, email, recordID, err := store.EmailVerificationByHash(ctx, "verifyhash1")
	if err != nil {
		t.Fatalf("EmailVerificationByHash: %v", err)
	}
	if userID != u.ID {
		t.Errorf("EmailVerificationByHash user id = %d, want %d", userID, u.ID)
	}
	if email != "new@example.com" {
		t.Errorf("EmailVerificationByHash email = %q, want normalized new@example.com", email)
	}
	if recordID == 0 {
		t.Fatal("EmailVerificationByHash returned a record id of 0")
	}

	if err := store.ConsumeEmailVerification(ctx, recordID); err != nil {
		t.Fatalf("ConsumeEmailVerification: %v", err)
	}
	if _, _, _, err := store.EmailVerificationByHash(ctx, "verifyhash1"); err != database.ErrNotFound {
		t.Errorf("EmailVerificationByHash(consumed) err = %v, want database.ErrNotFound", err)
	}

	// Idempotency: consuming twice must not error.
	if err := store.ConsumeEmailVerification(ctx, recordID); err != nil {
		t.Errorf("ConsumeEmailVerification (already consumed): %v", err)
	}
}

func TestPurgeExpiredGrants(t *testing.T) {
	store := NewStore(newAuthTestDB(t))
	ctx := context.Background()
	u := newTestUser(t, store, "purgegrantuser", "purgegrantuser@example.com")

	if err := store.CreatePasswordReset(ctx, u.ID, "grantexpired", 1); err != nil {
		t.Fatalf("CreatePasswordReset(expired): %v", err)
	}
	if err := store.CreatePasswordReset(ctx, u.ID, "grantlive", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatalf("CreatePasswordReset(live): %v", err)
	}
	if err := store.CreateEmailVerification(ctx, u.ID, "grant@example.com", "grantverifexpired", 1); err != nil {
		t.Fatalf("CreateEmailVerification(expired): %v", err)
	}

	if err := store.PurgeExpiredGrants(ctx); err != nil {
		t.Fatalf("PurgeExpiredGrants: %v", err)
	}

	if _, _, err := store.PasswordResetByHash(ctx, "grantexpired"); err == nil {
		t.Error("expired password reset survived PurgeExpiredGrants")
	}
	if _, _, err := store.PasswordResetByHash(ctx, "grantlive"); err != nil {
		t.Error("live password reset was purged by PurgeExpiredGrants")
	}
	if _, _, _, err := store.EmailVerificationByHash(ctx, "grantverifexpired"); err == nil {
		t.Error("expired email verification survived PurgeExpiredGrants")
	}

	// Idempotency: purging again must not error.
	if err := store.PurgeExpiredGrants(ctx); err != nil {
		t.Errorf("PurgeExpiredGrants (second run): %v", err)
	}
}
