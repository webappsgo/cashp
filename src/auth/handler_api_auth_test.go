package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMailer captures every outbound message instead of talking to real SMTP,
// so tests can pull the plaintext verification/reset token out of the link
// exactly like a user would from their inbox.
type fakeMailer struct {
	mu   sync.Mutex
	sent []mailMessage
}

type mailMessage struct {
	To, Subject, Body string
}

func (m *fakeMailer) Send(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, mailMessage{To: to, Subject: subject, Body: body})
	return nil
}

// last returns the most recently sent message, or fails the test if none was sent.
func (m *fakeMailer) last(t *testing.T) mailMessage {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		t.Fatal("fakeMailer: no message was sent")
	}
	return m.sent[len(m.sent)-1]
}

// tokenFromLink extracts the "token" query parameter from the first URL found
// in a mailed message body.
func tokenFromLink(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, "http")
	if i < 0 {
		t.Fatalf("mail body has no link: %q", body)
	}
	link := strings.Fields(body[i:])[0]
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", link, err)
	}
	tok := u.Query().Get("token")
	if tok == "" {
		t.Fatalf("link %q has no token param", link)
	}
	return tok
}

// newAPIAuthService builds a real Service plus a fakeMailer capable of
// capturing verification/reset links, so email-gated flows can be exercised
// end-to-end rather than skipped.
func newAPIAuthService(t *testing.T, mutate func(*Config)) (*Service, *fakeMailer) {
	t.Helper()
	db := newAuthTestDB(t)
	cfg := DefaultConfig()
	cfg.RequireEmailVerification = false
	if mutate != nil {
		mutate(&cfg)
	}
	mailer := &fakeMailer{}
	svc, err := New(Options{Store: NewStore(db), Config: cfg, Mailer: mailer})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, mailer
}

// authedRequest builds a request carrying method/path/JSON-body plus an
// Accept header forcing the JSON envelope, and runs it through the real
// RequireUser middleware using the given session cookie value — this
// exercises the actual unauthenticated-rejected / authenticated-accepted
// flow rather than hand-planting a user in context.
func doHandler(svc *Service, h http.HandlerFunc, method, path, sessionToken, body string, protect bool) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.Header.Set("Accept", "application/json")
	if sessionToken != "" {
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
	}
	if protect {
		svc.RequireUser(h).ServeHTTP(w, r)
	} else {
		h.ServeHTTP(w, r)
	}
	return w
}

// doHandlerAt is doHandler with an explicit caller IP, so tests that call a
// rate-limited endpoint several times for unrelated reasons (not testing the
// limiter itself) don't cross-pollute each other's quota.
func doHandlerAt(svc *Service, h http.HandlerFunc, method, path, sessionToken, body string, protect bool, ip string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.Header.Set("Accept", "application/json")
	r.RemoteAddr = ip + ":1234"
	if sessionToken != "" {
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
	}
	if protect {
		svc.RequireUser(h).ServeHTTP(w, r)
	} else {
		h.ServeHTTP(w, r)
	}
	return w
}

func decodeOK(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	var env struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("Unmarshal envelope: %v (body=%s)", err, w.Body.String())
	}
	if !env.OK {
		t.Fatalf("ok=false, want true (body=%s)", w.Body.String())
	}
	if dst != nil {
		if err := json.Unmarshal(env.Data, dst); err != nil {
			t.Fatalf("Unmarshal data: %v", err)
		}
	}
}

func decodeFail(t *testing.T, w *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	var env struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("Unmarshal envelope: %v (body=%s)", err, w.Body.String())
	}
	if env.OK {
		t.Fatalf("ok=true, want false (body=%s)", w.Body.String())
	}
	return env.Error, env.Message
}

// TestHandleRegisterAndCheckName covers account creation via the real HTTP
// handler (invalid payload, success + session cookie issuance, duplicate
// name rejected) and the anti-enumeration name-availability probe.
func TestHandleRegisterAndCheckName(t *testing.T) {
	svc, _ := newAPIAuthService(t, nil)

	t.Run("rejects invalid payload", func(t *testing.T) {
		w := doHandler(svc, svc.HandleRegister, http.MethodPost, "/api/v1/register", "",
			`{"username":"","email":"not-an-email","password":"x"}`, false)
		if w.Code < 400 {
			t.Errorf("status = %d, want 4xx", w.Code)
		}
	})

	t.Run("creates account and issues a session cookie", func(t *testing.T) {
		w := doHandler(svc, svc.HandleRegister, http.MethodPost, "/api/v1/register", "",
			`{"username":"newbie","email":"newbie@example.com","password":"a-good-password"}`, false)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		if len(w.Result().Cookies()) == 0 {
			t.Error("HandleRegister did not set a session cookie")
		}
		var pub PublicUser
		decodeOK(t, w, &pub)
		if pub.Username != "newbie" {
			t.Errorf("Username = %q, want newbie", pub.Username)
		}
	})

	t.Run("rejects a duplicate username", func(t *testing.T) {
		w := doHandler(svc, svc.HandleRegister, http.MethodPost, "/api/v1/register", "",
			`{"username":"newbie","email":"another@example.com","password":"a-good-password"}`, false)
		if w.Code < 400 {
			t.Errorf("status = %d, want 4xx for a duplicate username", w.Code)
		}
	})

	t.Run("check-name reports the same name unavailable now", func(t *testing.T) {
		w := doHandler(svc, svc.HandleCheckName, http.MethodGet, "/api/v1/check-name?name=newbie", "", "", false)
		// Handler writes its own failure via fail(); a taken name errors.
		if w.Code == http.StatusOK {
			t.Error("check-name reported a taken name as available")
		}
	})

	t.Run("check-name reports a free name available", func(t *testing.T) {
		w := doHandler(svc, svc.HandleCheckName, http.MethodGet, "/api/v1/check-name?name=totally-free-name", "", "", false)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var q checkNameQuery
		decodeOK(t, w, &q)
		if !q.Available {
			t.Error("Available = false, want true for a free name")
		}
	})
}

// TestHandleLoginLogoutAndMe exercises the required auth-flow shape:
// unauthenticated rejected -> login -> session-based access -> logout is
// idempotent -> access rejected again after logout.
func TestHandleLoginLogoutAndMe(t *testing.T) {
	svc, _ := newAPIAuthService(t, nil)
	u := registerTestUser(t, svc, "loginuser", "loginuser@example.com")
	_ = u

	t.Run("HandleMe rejects unauthenticated", func(t *testing.T) {
		w := doHandler(svc, svc.HandleMe, http.MethodGet, "/api/v1/me", "", "", true)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("wrong password rejected", func(t *testing.T) {
		w := doHandler(svc, svc.HandleLogin, http.MethodPost, "/api/v1/login", "",
			`{"login":"loginuser","password":"totally-wrong"}`, false)
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 4xx", w.Code)
		}
	})

	var sessionToken string
	t.Run("correct login issues a session", func(t *testing.T) {
		w := doHandler(svc, svc.HandleLogin, http.MethodPost, "/api/v1/login", "",
			`{"login":"loginuser","password":"a-good-password"}`, false)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		cookies := w.Result().Cookies()
		if len(cookies) == 0 {
			t.Fatal("HandleLogin did not set a session cookie")
		}
		for _, c := range cookies {
			if c.Name == SessionCookieName {
				sessionToken = c.Value
			}
		}
		if sessionToken == "" {
			t.Fatal("no cashp_session cookie among the response cookies")
		}
	})

	t.Run("HandleMe accepts the session", func(t *testing.T) {
		w := doHandler(svc, svc.HandleMe, http.MethodGet, "/api/v1/me", sessionToken, "", true)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var pub PublicUser
		decodeOK(t, w, &pub)
		if pub.Username != "loginuser" {
			t.Errorf("Username = %q, want loginuser", pub.Username)
		}
	})

	t.Run("logout is idempotent", func(t *testing.T) {
		w1 := doHandler(svc, svc.HandleLogout, http.MethodPost, "/api/v1/logout", sessionToken, "", false)
		if w1.Code != http.StatusOK {
			t.Fatalf("first logout status = %d, want 200", w1.Code)
		}
		w2 := doHandler(svc, svc.HandleLogout, http.MethodPost, "/api/v1/logout", sessionToken, "", false)
		if w2.Code != http.StatusOK {
			t.Fatalf("second logout status = %d, want 200 (idempotent)", w2.Code)
		}
	})

	t.Run("HandleMe rejects after logout", func(t *testing.T) {
		w := doHandler(svc, svc.HandleMe, http.MethodGet, "/api/v1/me", sessionToken, "", true)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 after logout", w.Code)
		}
	})
}

// TestHandleUpdateMeAndDeleteMe covers profile editing and the
// password-confirmed account-deletion path (wrong password rejected first).
func TestHandleUpdateMeAndDeleteMe(t *testing.T) {
	svc, _ := newAPIAuthService(t, nil)
	u, sessionToken, aerr := svc.Register(context.Background(), RegisterInput{
		Username: "editme", Email: "editme@example.com", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Register: %v", aerr)
	}
	_ = u

	t.Run("update profile", func(t *testing.T) {
		w := doHandler(svc, svc.HandleUpdateMe, http.MethodPatch, "/api/v1/me", sessionToken,
			`{"display_name":"Edited Name","bio":"hello"}`, true)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var pub PublicUser
		decodeOK(t, w, &pub)
		if pub.DisplayName != "Edited Name" {
			t.Errorf("DisplayName = %q, want Edited Name", pub.DisplayName)
		}
	})

	t.Run("delete rejects wrong password", func(t *testing.T) {
		w := doHandler(svc, svc.HandleDeleteMe, http.MethodPost, "/api/v1/me/delete", sessionToken,
			`{"password":"not-the-password"}`, true)
		if w.Code < 400 {
			t.Errorf("status = %d, want 4xx for wrong password", w.Code)
		}
	})

	t.Run("delete succeeds with the right password", func(t *testing.T) {
		w := doHandler(svc, svc.HandleDeleteMe, http.MethodPost, "/api/v1/me/delete", sessionToken,
			`{"password":"a-good-password"}`, true)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("session is dead after account deletion", func(t *testing.T) {
		w := doHandler(svc, svc.HandleMe, http.MethodGet, "/api/v1/me", sessionToken, "", true)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 after account deletion", w.Code)
		}
	})
}

// TestHandleChangePassword covers the wrong-current-password rejection and
// the successful rotation, which must re-issue a usable session cookie.
func TestHandleChangePassword(t *testing.T) {
	svc, _ := newAPIAuthService(t, nil)
	_, sessionToken, aerr := svc.Register(context.Background(), RegisterInput{
		Username: "pwuser", Email: "pwuser@example.com", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Register: %v", aerr)
	}

	t.Run("rejects wrong current password", func(t *testing.T) {
		w := doHandler(svc, svc.HandleChangePassword, http.MethodPost, "/api/v1/me/password", sessionToken,
			`{"current_password":"nope","new_password":"another-good-password"}`, true)
		if w.Code < 400 {
			t.Errorf("status = %d, want 4xx", w.Code)
		}
	})

	var newToken string
	t.Run("rotates on the right current password", func(t *testing.T) {
		w := doHandler(svc, svc.HandleChangePassword, http.MethodPost, "/api/v1/me/password", sessionToken,
			`{"current_password":"a-good-password","new_password":"another-good-password"}`, true)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		for _, c := range w.Result().Cookies() {
			if c.Name == SessionCookieName {
				newToken = c.Value
			}
		}
		if newToken == "" {
			t.Fatal("HandleChangePassword did not re-issue a session cookie")
		}
	})

	t.Run("old session is invalidated by the rotation", func(t *testing.T) {
		w := doHandler(svc, svc.HandleMe, http.MethodGet, "/api/v1/me", sessionToken, "", true)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 (old session should have been revoked)", w.Code)
		}
	})

	t.Run("new session works", func(t *testing.T) {
		w := doHandler(svc, svc.HandleMe, http.MethodGet, "/api/v1/me", newToken, "", true)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 with the re-issued session", w.Code)
		}
	})
}

// TestHandlePasswordResetFlow covers the anti-enumeration request step (same
// response for a known and unknown address) and the confirm step, including
// a bad-token rejection and successful login with the new password.
func TestHandlePasswordResetFlow(t *testing.T) {
	svc, mailer := newAPIAuthService(t, nil)
	registerTestUser(t, svc, "resetuser", "resetuser@example.com")

	// Each subtest below uses a distinct source IP: HandleRequestPasswordReset
	// and HandleConfirmPasswordReset share the LimitPasswordReset bucket
	// (3/hour per IP), and these subtests exercise distinct behaviors, not
	// the limiter itself (that's TestRateLimitAndRateLimitByMethod's job).

	t.Run("request reset succeeds for a known address", func(t *testing.T) {
		w := doHandlerAt(svc, svc.HandleRequestPasswordReset, http.MethodPost, "/api/v1/password/reset", "",
			`{"email":"resetuser@example.com"}`, false, "198.51.100.1")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("request reset gives the identical response for an unknown address", func(t *testing.T) {
		w := doHandlerAt(svc, svc.HandleRequestPasswordReset, http.MethodPost, "/api/v1/password/reset", "",
			`{"email":"nobody-here@example.com"}`, false, "198.51.100.2")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (anti-enumeration)", w.Code)
		}
	})

	t.Run("confirm rejects a bad token", func(t *testing.T) {
		w := doHandlerAt(svc, svc.HandleConfirmPasswordReset, http.MethodPost, "/api/v1/password/reset/confirm", "",
			`{"token":"not-a-real-token","new_password":"brand-new-password"}`, false, "198.51.100.3")
		if w.Code < 400 {
			t.Errorf("status = %d, want 4xx for a bad token", w.Code)
		}
	})

	t.Run("confirm succeeds with the mailed token", func(t *testing.T) {
		msg := mailer.last(t)
		token := tokenFromLink(t, msg.Body)
		w := doHandlerAt(svc, svc.HandleConfirmPasswordReset, http.MethodPost, "/api/v1/password/reset/confirm", "",
			`{"token":"`+token+`","new_password":"brand-new-password"}`, false, "198.51.100.4")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("login works with the new password", func(t *testing.T) {
		w := doHandlerAt(svc, svc.HandleLogin, http.MethodPost, "/api/v1/login", "",
			`{"login":"resetuser","password":"brand-new-password"}`, false, "198.51.100.5")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("consumed reset token cannot be reused", func(t *testing.T) {
		msg := mailer.last(t)
		token := tokenFromLink(t, msg.Body)
		w := doHandlerAt(svc, svc.HandleConfirmPasswordReset, http.MethodPost, "/api/v1/password/reset/confirm", "",
			`{"token":"`+token+`","new_password":"yet-another-password"}`, false, "198.51.100.6")
		if w.Code < 400 {
			t.Errorf("status = %d, want 4xx (reset token must not be reusable)", w.Code)
		}
	})
}

// TestHandleVerifyEmailAndResend covers the mailed-confirmation-link flow
// with RequireEmailVerification on, plus the authenticated resend path.
func TestHandleVerifyEmailAndResend(t *testing.T) {
	svc, mailer := newAPIAuthService(t, func(c *Config) { c.RequireEmailVerification = true })
	_, sessionToken, aerr := svc.Register(context.Background(), RegisterInput{
		Username: "verifyuser", Email: "verifyuser@example.com", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Register: %v", aerr)
	}

	t.Run("resend requires auth", func(t *testing.T) {
		w := doHandler(svc, svc.HandleResendVerification, http.MethodPost, "/api/v1/verify/resend", "", "", true)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("verify rejects a bad token", func(t *testing.T) {
		w := doHandler(svc, svc.HandleVerifyEmail, http.MethodGet, "/api/v1/verify?token=garbage", "", "", false)
		if w.Code < 400 {
			t.Errorf("status = %d, want 4xx for a bad token", w.Code)
		}
	})

	t.Run("verify succeeds with the mailed token", func(t *testing.T) {
		msg := mailer.last(t)
		token := tokenFromLink(t, msg.Body)
		w := doHandler(svc, svc.HandleVerifyEmail, http.MethodGet, "/api/v1/verify?token="+token, "", "", false)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("resend after verification is a benign no-op", func(t *testing.T) {
		w := doHandler(svc, svc.HandleResendVerification, http.MethodPost, "/api/v1/verify/resend", sessionToken, "", true)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})
}

// TestHandleTOTPFlow covers two-factor enrolment end to end: begin ->
// confirm rejects a wrong code -> confirm accepts the real code -> disable
// requires both the password and a fresh code.
func TestHandleTOTPFlow(t *testing.T) {
	svc, _ := newAPIAuthService(t, nil)
	_, sessionToken, aerr := svc.Register(context.Background(), RegisterInput{
		Username: "totpuser", Email: "totpuser@example.com", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Register: %v", aerr)
	}

	var secret string
	t.Run("begin enrolment", func(t *testing.T) {
		w := doHandler(svc, svc.HandleBeginTOTP, http.MethodPost, "/api/v1/totp/begin", sessionToken, "", true)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var setup totpSetup
		decodeOK(t, w, &setup)
		if setup.Secret == "" {
			t.Fatal("BeginTOTP returned an empty secret")
		}
		secret = setup.Secret
	})

	t.Run("confirm rejects a wrong code", func(t *testing.T) {
		w := doHandler(svc, svc.HandleConfirmTOTP, http.MethodPost, "/api/v1/totp/confirm", sessionToken,
			`{"code":"000000"}`, true)
		if w.Code < 400 {
			t.Errorf("status = %d, want 4xx for a wrong code", w.Code)
		}
	})

	t.Run("confirm accepts the real code", func(t *testing.T) {
		code, err := TOTPCode(secret, time.Now())
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		w := doHandler(svc, svc.HandleConfirmTOTP, http.MethodPost, "/api/v1/totp/confirm", sessionToken,
			`{"code":"`+code+`"}`, true)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("disable rejects a wrong password", func(t *testing.T) {
		code, err := TOTPCode(secret, time.Now())
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		w := doHandler(svc, svc.HandleDisableTOTP, http.MethodPost, "/api/v1/totp/disable", sessionToken,
			`{"password":"wrong","code":"`+code+`"}`, true)
		if w.Code < 400 {
			t.Errorf("status = %d, want 4xx for a wrong password", w.Code)
		}
	})

	t.Run("disable succeeds with password and a fresh code", func(t *testing.T) {
		code, err := TOTPCode(secret, time.Now())
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		w := doHandler(svc, svc.HandleDisableTOTP, http.MethodPost, "/api/v1/totp/disable", sessionToken,
			`{"password":"a-good-password","code":"`+code+`"}`, true)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})
}

// TestHandleSessionsFlow covers listing, revoking one, and revoking all
// sessions, including idempotency of revoking the same session twice.
func TestHandleSessionsFlow(t *testing.T) {
	svc, _ := newAPIAuthService(t, nil)
	_, sessionToken, aerr := svc.Register(context.Background(), RegisterInput{
		Username: "sessuser", Email: "sessuser@example.com", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Register: %v", aerr)
	}
	_, secondToken, aerr := svc.Login(context.Background(), LoginInput{
		Identifier: "sessuser", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Login (second session): %v", aerr)
	}
	_ = secondToken

	var sessionID int64
	t.Run("list sessions", func(t *testing.T) {
		w := doHandler(svc, svc.HandleListSessions, http.MethodGet, "/api/v1/sessions", sessionToken, "", true)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var rows []PublicSession
		decodeOK(t, w, &rows)
		if len(rows) < 2 {
			t.Fatalf("len(rows) = %d, want >= 2", len(rows))
		}
		for _, row := range rows {
			if !row.Current {
				sessionID = row.ID
			}
		}
		if sessionID == 0 {
			t.Fatal("could not find the non-current session to revoke")
		}
	})

	t.Run("revoke one session", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/x", nil)
		r.SetPathValue("id", itoaHelper(sessionID))
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
		w := httptest.NewRecorder()
		svc.RequireUser(http.HandlerFunc(svc.HandleRevokeSession)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("revoking an unknown session id is a benign no-op (DELETE is idempotent)", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/x", nil)
		r.SetPathValue("id", "999999999")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
		w := httptest.NewRecorder()
		svc.RequireUser(http.HandlerFunc(svc.HandleRevokeSession)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (DELETE of a nonexistent id must be idempotent)", w.Code)
		}
	})

	t.Run("revoke all sessions clears the caller's own cookie too", func(t *testing.T) {
		w := doHandler(svc, svc.HandleRevokeAllSessions, http.MethodPost, "/api/v1/sessions/revoke-all", sessionToken, "", true)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		w2 := doHandler(svc, svc.HandleMe, http.MethodGet, "/api/v1/me", sessionToken, "", true)
		if w2.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 after revoke-all", w2.Code)
		}
	})
}

// TestHandleTokensFlow covers listing, creating, and revoking API tokens,
// including idempotency of revoking the same token twice.
func TestHandleTokensFlow(t *testing.T) {
	svc, _ := newAPIAuthService(t, nil)
	_, sessionToken, aerr := svc.Register(context.Background(), RegisterInput{
		Username: "tokuser", Email: "tokuser@example.com", Password: "a-good-password",
	})
	if aerr != nil {
		t.Fatalf("Register: %v", aerr)
	}

	t.Run("list starts empty", func(t *testing.T) {
		w := doHandler(svc, svc.HandleListTokens, http.MethodGet, "/api/v1/tokens", sessionToken, "", true)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var rows []PublicToken
		decodeOK(t, w, &rows)
		if len(rows) != 0 {
			t.Errorf("len(rows) = %d, want 0", len(rows))
		}
	})

	var tokenID int64
	t.Run("create a token", func(t *testing.T) {
		w := doHandler(svc, svc.HandleCreateToken, http.MethodPost, "/api/v1/tokens", sessionToken,
			`{"name":"ci","scopes":["*"]}`, true)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		var pub PublicToken
		decodeOK(t, w, &pub)
		if pub.Token == "" {
			t.Error("HandleCreateToken did not return the plaintext token")
		}
		tokenID = pub.ID
	})

	t.Run("revoke the token", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/tokens/x", nil)
		r.SetPathValue("id", itoaHelper(tokenID))
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
		w := httptest.NewRecorder()
		svc.RequireUser(http.HandlerFunc(svc.HandleRevokeToken)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("revoking the same token twice is safe", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/tokens/x", nil)
		r.SetPathValue("id", itoaHelper(tokenID))
		r.Header.Set("Accept", "application/json")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
		w := httptest.NewRecorder()
		svc.RequireUser(http.HandlerFunc(svc.HandleRevokeToken)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (revoke must be idempotent)", w.Code)
		}
	})
}

// itoaHelper renders a path-value id as this test file's httptest requests need.
func itoaHelper(id int64) string {
	return strconv.FormatInt(id, 10)
}
