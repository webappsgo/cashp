package notify

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal in-process SMTP server spoken over net.Pipe, so the
// SMTP channel can be exercised end to end without a listening socket.
type fakeSMTP struct {
	mu       sync.Mutex
	authOK   bool
	rejectTo bool
	message  string
	sessions int
}

// dial returns a dialer that answers every address with a new fake session.
func (f *fakeSMTP) dial(_ context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	f.mu.Lock()
	f.sessions++
	f.mu.Unlock()
	go f.serve(server)
	return client, nil
}

// serve runs one SMTP session until QUIT or the peer hangs up.
func (f *fakeSMTP) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	if _, err := conn.Write([]byte("220 fake ESMTP ready\r\n")); err != nil {
		return
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		verb := strings.ToUpper(strings.TrimSpace(line))
		if space := strings.IndexByte(verb, ' '); space > 0 {
			verb = verb[:space]
		}

		var reply string
		switch verb {
		case "EHLO", "HELO":
			reply = "250-fake greets you\r\n250 AUTH PLAIN\r\n"
		case "AUTH":
			f.mu.Lock()
			ok := f.authOK
			f.mu.Unlock()
			reply = "535 5.7.8 authentication failed\r\n"
			if ok {
				reply = "235 2.7.0 accepted\r\n"
			}
		case "NOOP", "RSET", "MAIL":
			reply = "250 2.0.0 ok\r\n"
		case "RCPT":
			f.mu.Lock()
			reject := f.rejectTo
			f.mu.Unlock()
			reply = "250 2.1.5 ok\r\n"
			if reject {
				reply = "550 5.1.1 no such user\r\n"
			}
		case "DATA":
			if _, err := conn.Write([]byte("354 end with <CRLF>.<CRLF>\r\n")); err != nil {
				return
			}
			var body strings.Builder
			for {
				dataLine, readErr := reader.ReadString('\n')
				if readErr != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				body.WriteString(dataLine)
			}
			f.mu.Lock()
			f.message = body.String()
			f.mu.Unlock()
			reply = "250 2.0.0 queued\r\n"
		case "QUIT":
			_, _ = conn.Write([]byte("221 2.0.0 bye\r\n"))
			return
		default:
			reply = "500 5.5.1 unrecognized\r\n"
		}

		if _, err := conn.Write([]byte(reply)); err != nil {
			return
		}
	}
}

// delivered returns the captured message body.
func (f *fakeSMTP) delivered() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.message
}

// smtpTestSettings returns settings pointed at the fake server. localhost is
// required because net/smtp refuses PLAIN auth on an unencrypted connection
// to anything else.
func smtpTestSettings() SMTPSettings {
	return SMTPSettings{
		Host:      "localhost",
		Port:      25,
		TLS:       TLSNone,
		FromEmail: "no-reply@example.com",
		FromName:  "cashp",
	}
}

func TestSMTPMaskedHidesThePassword(t *testing.T) {
	settings := smtpTestSettings()
	settings.Username = "mailer"
	settings.Password = "super-secret-value"

	masked := settings.Masked()
	if strings.Contains(masked.Password, "super-secret-value") {
		t.Fatalf("the password survived masking: %q", masked.Password)
	}
	if masked.Password == "" {
		t.Fatal("masking must leave a placeholder so the UI can show a value is set")
	}
	if masked.Username != "mailer" || masked.Host != "localhost" {
		t.Fatal("masking must not disturb the non-secret fields")
	}
	if settings.Password != "super-secret-value" {
		t.Fatal("Masked must not mutate the receiver")
	}

	channel := NewSMTPChannel(settings, (&fakeSMTP{}).dial, time.Now)
	if strings.Contains(channel.Settings().Password, "super-secret-value") {
		t.Fatal("Settings must never return the plaintext password")
	}
}

func TestSMTPConfigureKeepsStoredPasswordOnEmptySubmit(t *testing.T) {
	settings := smtpTestSettings()
	settings.Username = "mailer"
	settings.Password = "stored-secret"
	channel := NewSMTPChannel(settings, (&fakeSMTP{}).dial, time.Now)

	updated := smtpTestSettings()
	updated.Username = "mailer"
	updated.Host = "smtp.example.com"
	channel.Configure(updated)

	if err := channel.Validate(); err != nil {
		t.Fatalf("an empty password submit must keep the stored secret: %v", err)
	}
}

func TestSMTPValidateRejectsIncompleteSettings(t *testing.T) {
	cases := map[string]func(*SMTPSettings){
		"no host":      func(s *SMTPSettings) { s.Host = "" },
		"bad port":     func(s *SMTPSettings) { s.Port = 70000 },
		"no sender":    func(s *SMTPSettings) { s.FromEmail = "" },
		"bad sender":   func(s *SMTPSettings) { s.FromEmail = "not-an-address" },
		"bad tls mode": func(s *SMTPSettings) { s.TLS = "maybe" },
		"user no pass": func(s *SMTPSettings) { s.Username = "mailer" },
	}
	for name, mutate := range cases {
		settings := smtpTestSettings()
		mutate(&settings)
		channel := NewSMTPChannel(settings, (&fakeSMTP{}).dial, time.Now)
		if err := channel.Validate(); err == nil {
			t.Fatalf("%s: expected validation to fail", name)
		}
	}

	good := NewSMTPChannel(smtpTestSettings(), (&fakeSMTP{}).dial, time.Now)
	if err := good.Validate(); err != nil {
		t.Fatalf("complete settings must validate: %v", err)
	}
}

func TestSMTPTestSucceedsAgainstAServer(t *testing.T) {
	server := &fakeSMTP{}
	channel := NewSMTPChannel(smtpTestSettings(), server.dial, time.Now)

	result := channel.Test(context.Background())
	if result.Err != nil {
		t.Fatalf("test: %v", result.Err)
	}
	if !result.OK() {
		t.Fatalf("expected a passing test, got %+v", result)
	}
	if result.Authenticated {
		t.Fatal("an unauthenticated relay must not report authentication")
	}
	if !strings.Contains(result.Detail, "localhost:25") {
		t.Fatalf("the detail must name the server, got %q", result.Detail)
	}
}

func TestSMTPTestReportsAuthenticationFailure(t *testing.T) {
	server := &fakeSMTP{authOK: false}
	settings := smtpTestSettings()
	settings.Username = "mailer"
	settings.Password = "wrong"
	channel := NewSMTPChannel(settings, server.dial, time.Now)

	result := channel.Test(context.Background())
	if result.Err == nil {
		t.Fatal("rejected credentials must fail the test")
	}
	if !result.Connected {
		t.Fatal("the connection itself succeeded and must be reported as such")
	}
	if result.Authenticated || result.OK() {
		t.Fatalf("a rejected login must not pass: %+v", result)
	}
	if strings.Contains(result.Detail, "wrong") {
		t.Fatalf("the failure detail leaked the password: %q", result.Detail)
	}
}

func TestSMTPTestAuthenticatesWhenCredentialsAreAccepted(t *testing.T) {
	server := &fakeSMTP{authOK: true}
	settings := smtpTestSettings()
	settings.Username = "mailer"
	settings.Password = "right"
	channel := NewSMTPChannel(settings, server.dial, time.Now)

	result := channel.Test(context.Background())
	if result.Err != nil {
		t.Fatalf("test: %v", result.Err)
	}
	if !result.Authenticated || !result.OK() {
		t.Fatalf("expected an authenticated pass, got %+v", result)
	}
}

func TestSMTPSendDeliversTheRenderedMessage(t *testing.T) {
	server := &fakeSMTP{}
	channel := NewSMTPChannel(smtpTestSettings(), server.dial, time.Now)

	rendered := Rendered{
		ID:        "0193c0de-0000-7000-8000-000000000001",
		Event:     EventPasswordReset,
		Type:      TypeSecurity,
		Subject:   "Reset your password",
		Body:      "Open the link to reset the password for user@example.com.\n",
		Recipient: Recipient{Audience: AudienceUser, ID: "u1", Email: "user@example.com"},
	}
	if err := channel.Send(context.Background(), rendered); err != nil {
		t.Fatalf("send: %v", err)
	}

	message := server.delivered()
	for _, want := range []string{
		"To: user@example.com",
		"Subject: Reset your password",
		"X-Notification-Event: " + EventPasswordReset,
		"Auto-Submitted: auto-generated",
		"Open the link to reset the password",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("delivered message is missing %q:\n%s", want, message)
		}
	}
}

func TestSMTPSendRefusesAMessageWithNoAddress(t *testing.T) {
	server := &fakeSMTP{}
	channel := NewSMTPChannel(smtpTestSettings(), server.dial, time.Now)

	err := channel.Send(context.Background(), Rendered{Event: EventTest, Subject: "hi", Body: "hi\n"})
	if err == nil {
		t.Fatal("a message with no recipient address must be refused")
	}
	if server.sessions != 0 {
		t.Fatal("no connection may be opened for an unaddressable message")
	}
}

func TestSMTPSendSurfacesARejectedRecipient(t *testing.T) {
	server := &fakeSMTP{rejectTo: true}
	channel := NewSMTPChannel(smtpTestSettings(), server.dial, time.Now)

	rendered := Rendered{
		Event:     EventTest,
		Subject:   "hi",
		Body:      "hi\n",
		Recipient: Recipient{Audience: AudienceUser, ID: "u1", Email: "nobody@example.com"},
	}
	if err := channel.Send(context.Background(), rendered); err == nil {
		t.Fatal("a rejected recipient must surface as an error")
	}
}

func TestBuildMessageRejectsHeaderInjection(t *testing.T) {
	settings := smtpTestSettings()
	settings.FromName = "cashp\r\nBcc: attacker@example.com"

	rendered := Rendered{
		ID:        "id-1\r\nX-Injected: yes",
		Event:     EventTest,
		Subject:   "Hello\r\nBcc: attacker@example.com",
		Body:      "body\n",
		Recipient: Recipient{Email: "user@example.com\r\nCc: attacker@example.com"},
	}
	message := BuildMessage(settings, rendered, time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC))

	headers := message
	if split := strings.Index(message, "\r\n\r\n"); split >= 0 {
		headers = message[:split]
	}
	// A stripped CR or LF leaves the injected text inside the header it came
	// from, so the check is that no header LINE begins with the smuggled name.
	for _, line := range strings.Split(headers, "\r\n") {
		for _, forbidden := range []string{"Bcc:", "Cc:", "X-Injected:"} {
			if strings.HasPrefix(line, forbidden) {
				t.Fatalf("header injection produced a new %q header:\n%s", forbidden, headers)
			}
		}
	}
	if strings.Count(headers, "\n") != strings.Count(headers, "\r\n") {
		t.Fatalf("a bare newline survived in the headers: %q", headers)
	}
	if !strings.Contains(message, "Date: ") || !strings.Contains(message, "MIME-Version: 1.0") {
		t.Fatalf("the required headers are missing:\n%s", headers)
	}
}

func TestBuildMessageNormalisesBodyLineEndings(t *testing.T) {
	rendered := Rendered{
		Subject:   "Subject",
		Body:      "line one\nline two\r\nline three\n",
		Recipient: Recipient{Email: "user@example.com"},
	}
	message := BuildMessage(smtpTestSettings(), rendered, time.Now())

	split := strings.Index(message, "\r\n\r\n")
	if split < 0 {
		t.Fatalf("no header/body separator:\n%s", message)
	}
	body := message[split+4:]
	if strings.Contains(strings.ReplaceAll(body, "\r\n", ""), "\n") {
		t.Fatalf("every body line must end CRLF: %q", body)
	}
}

func TestDetectSMTPPrefersTheFirstAnsweringCandidate(t *testing.T) {
	server := &fakeSMTP{}
	dialer := func(ctx context.Context, network, address string) (net.Conn, error) {
		if strings.HasPrefix(address, "localhost:") {
			return server.dial(ctx, network, address)
		}
		return nil, &net.OpError{Op: "dial", Net: network, Err: errRefused{}}
	}

	host, port, ok := DetectSMTP(context.Background(), []string{"", "10.0.0.1", "localhost"}, dialer)
	if !ok {
		t.Fatal("the answering candidate must be detected")
	}
	if host != "localhost" {
		t.Fatalf("unexpected host %q", host)
	}
	if port != smtpDetectPorts[0] {
		t.Fatalf("the port sweep must start at %d, got %d", smtpDetectPorts[0], port)
	}
}

func TestDetectSMTPReturnsFalseWhenNothingAnswers(t *testing.T) {
	dialer := func(_ context.Context, network, _ string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: network, Err: errRefused{}}
	}
	if _, _, ok := DetectSMTP(context.Background(), []string{"10.0.0.1", "10.0.0.2"}, dialer); ok {
		t.Fatal("no relay available must not report a detection")
	}
}

func TestDetectionHostsFollowPriorityOrderWithoutDuplicates(t *testing.T) {
	hosts := DetectionHosts("172.17.0.1", "panel.example.com", "203.0.113.10")
	want := []string{
		"127.0.0.1",
		"172.17.0.1",
		"panel.example.com",
		"203.0.113.10",
		"mail.panel.example.com",
		"smtp.panel.example.com",
	}
	if len(hosts) != len(want) {
		t.Fatalf("expected %d candidates, got %v", len(want), hosts)
	}
	for i, host := range want {
		if hosts[i] != host {
			t.Fatalf("candidate %d is %q, want %q", i, hosts[i], host)
		}
	}

	if got := DetectionHosts("", "", ""); len(got) != 2 {
		t.Fatalf("an instance with no hints must still probe the two local candidates, got %v", got)
	}
}

func TestSMTPConfigSchemaDeclaresEnvVarsAndMasksTheSecret(t *testing.T) {
	channel := NewSMTPChannel(smtpTestSettings(), (&fakeSMTP{}).dial, time.Now)
	fields := map[string]Field{}
	for _, field := range channel.ConfigSchema() {
		if field.Help == "" {
			t.Fatalf("field %s has no [?] help text", field.Name)
		}
		fields[field.Name] = field
	}

	for name, env := range map[string]string{
		"host":       "SMTP_HOST",
		"port":       "SMTP_PORT",
		"username":   "SMTP_USERNAME",
		"password":   "SMTP_PASSWORD",
		"tls":        "SMTP_TLS",
		"from_email": "SMTP_FROM",
		"from_name":  "SMTP_FROM_NAME",
	} {
		field, ok := fields[name]
		if !ok {
			t.Fatalf("the schema is missing the %s field", name)
		}
		if field.EnvVar != env {
			t.Fatalf("field %s must be pre-filled from %s, got %q", name, env, field.EnvVar)
		}
	}

	if !fields["password"].Secret {
		t.Fatal("the password field must be marked secret")
	}
	if fields["host"].Secret || fields["from_email"].Secret {
		t.Fatal("non-secret fields must not be marked secret")
	}
}

func TestSMTPAutoEnablesAndOtherChannelsDoNot(t *testing.T) {
	server := &fakeSMTP{}
	channel := NewSMTPChannel(smtpTestSettings(), server.dial, time.Now)
	if !channel.AutoEnable() {
		t.Fatal("SMTP is the one outbound channel AI.md auto-enables")
	}

	registry := NewRegistry(fixedClock(time.Millisecond))
	if err := registry.Register(channel); err != nil {
		t.Fatalf("register: %v", err)
	}
	if state, _ := registry.State(ChannelSMTP); state != StateDisabled {
		t.Fatalf("a freshly registered channel must be DISABLED, got %s", state)
	}
	if _, err := registry.Test(context.Background(), ChannelSMTP); err != nil {
		t.Fatalf("test: %v", err)
	}
	if state, _ := registry.State(ChannelSMTP); state != StateActive {
		t.Fatalf("a passing SMTP test must activate email, got %s", state)
	}
}

func TestSMTPAcceptsOnlyAddressableMessages(t *testing.T) {
	channel := NewSMTPChannel(smtpTestSettings(), (&fakeSMTP{}).dial, time.Now)
	if channel.Accepts(Rendered{Event: EventTest}) {
		t.Fatal("SMTP must not accept a message with no address")
	}
	if !channel.Accepts(Rendered{Event: EventTest, Recipient: Recipient{Email: "user@example.com"}}) {
		t.Fatal("SMTP must accept an addressed message")
	}
}

func TestSMTPHelpIsSelfContained(t *testing.T) {
	channel := NewSMTPChannel(smtpTestSettings(), (&fakeSMTP{}).dial, time.Now)
	help := channel.Help()
	if help.Summary == "" || len(help.Setup) == 0 || len(help.Troubleshooting) == 0 {
		t.Fatal("the SMTP channel must ship a complete embedded setup guide")
	}
	for _, entry := range help.Troubleshooting {
		if entry.Symptom == "" || entry.Resolution == "" {
			t.Fatalf("incomplete troubleshooting entry %+v", entry)
		}
	}
	if help.Comparison.Speed == "" || help.Comparison.Reliability == "" || help.Comparison.Pricing == "" {
		t.Fatal("the comparison row must be complete")
	}
}

// errRefused stands in for a refused connection in the detection tests.
type errRefused struct{}

func (errRefused) Error() string { return "connection refused" }
