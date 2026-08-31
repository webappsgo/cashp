package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// SMTP TLS modes, matching server.notifications.email.smtp.tls in the
// configuration file.
const (
	// TLSAuto picks implicit TLS on port 465 and STARTTLS elsewhere,
	// falling back to a plain session when the server offers no STARTTLS.
	TLSAuto = "auto"
	// TLSStartTLS requires an upgrade on a plain connection.
	TLSStartTLS = "starttls"
	// TLSImplicit wraps the connection in TLS from the first byte.
	TLSImplicit = "tls"
	// TLSNone forbids encryption. It is only sensible for a loopback relay.
	TLSNone = "none"
)

// smtpDetectPorts is the port sweep applied to every auto-detection host,
// in the order AI.md PART 18 -> "SMTP Auto-Detection" lists them.
var smtpDetectPorts = []int{25, 465, 587}

// smtpDialTimeout bounds one auto-detection probe. The sweep visits up to
// seven hosts, so a long timeout would stall startup.
const smtpDialTimeout = 2 * time.Second

// smtpSendTimeout bounds a real delivery, which may involve authentication
// and a TLS handshake against a remote server.
const smtpSendTimeout = 30 * time.Second

// SMTPSettings is the SMTP channel configuration. It mirrors the config
// file section; the password is held in memory only and is never rendered
// into a status payload.
type SMTPSettings struct {
	// Host is the mail server hostname. Empty triggers auto-detection.
	Host string
	// Port is the mail server port.
	Port int
	// Username is the SMTP AUTH user, empty for an unauthenticated relay.
	Username string
	// Password is the SMTP AUTH password.
	Password string
	// TLS is one of TLSAuto, TLSStartTLS, TLSImplicit or TLSNone.
	TLS string
	// FromName is the sender display name.
	FromName string
	// FromEmail is the envelope and header sender address.
	FromEmail string
	// ReplyTo is added as a Reply-To header when set.
	ReplyTo string
	// Detected records that Host and Port came from auto-detection rather
	// than from the operator.
	Detected bool
}

// Masked returns the settings with the password replaced, safe to render
// into an API response or a log line.
func (s SMTPSettings) Masked() SMTPSettings {
	if s.Password != "" {
		s.Password = security.MaskSecret(s.Password)
	}
	return s
}

// Address returns the host:port dial target.
func (s SMTPSettings) Address() string {
	return net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
}

// SMTPChannel delivers email. It is the one channel AI.md grants special
// behaviour: it reads environment variables as initial values, it can
// auto-detect a local relay, and a passing test activates it without a
// separate operator action.
type SMTPChannel struct {
	mu       sync.RWMutex
	settings SMTPSettings
	dial     func(ctx context.Context, network, address string) (net.Conn, error)
	now      func() time.Time
}

// ChannelSMTP is the registry name of the SMTP channel.
const ChannelSMTP = "smtp"

// NewSMTPChannel returns an SMTP channel. dialer may be nil, in which case
// a bounded net.Dialer is used; tests inject their own to reach a local
// fake server without touching the network.
func NewSMTPChannel(settings SMTPSettings, dialer func(ctx context.Context, network, address string) (net.Conn, error), now func() time.Time) *SMTPChannel {
	if dialer == nil {
		d := &net.Dialer{Timeout: smtpDialTimeout}
		dialer = d.DialContext
	}
	if now == nil {
		now = time.Now
	}
	if settings.TLS == "" {
		settings.TLS = TLSAuto
	}
	if settings.Port == 0 {
		settings.Port = 587
	}
	return &SMTPChannel{settings: settings, dial: dialer, now: now}
}

// Name implements Channel.
func (c *SMTPChannel) Name() string { return ChannelSMTP }

// Category implements Channel.
func (c *SMTPChannel) Category() string { return CategoryEmail }

// AutoEnable implements Channel. AI.md PART 18 enables email as soon as a
// handshake succeeds, so a passing test activates this channel directly.
func (c *SMTPChannel) AutoEnable() bool { return true }

// Accepts implements Channel: SMTP only handles messages that resolved to
// a recipient with an address.
func (c *SMTPChannel) Accepts(r Rendered) bool { return r.Recipient.Email != "" }

// Settings returns the current settings with the password masked.
func (c *SMTPChannel) Settings() SMTPSettings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.settings.Masked()
}

// Configure replaces the channel settings. An empty password keeps the
// stored one, so an operator can save the form without retyping a secret
// that the UI only ever showed masked.
func (c *SMTPChannel) Configure(settings SMTPSettings) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if settings.Password == "" {
		settings.Password = c.settings.Password
	}
	if settings.TLS == "" {
		settings.TLS = TLSAuto
	}
	if settings.Port == 0 {
		settings.Port = 587
	}
	c.settings = settings
}

// Validate implements Channel.
func (c *SMTPChannel) Validate() error {
	c.mu.RLock()
	settings := c.settings
	c.mu.RUnlock()

	switch {
	case settings.Host == "":
		return errors.New(errors.CodeValidation, http.StatusBadRequest, "smtp host is not configured")
	case settings.Port <= 0 || settings.Port > 65535:
		return errors.New(errors.CodeValidation, http.StatusBadRequest, "smtp port is out of range")
	case settings.FromEmail == "":
		return errors.New(errors.CodeValidation, http.StatusBadRequest, "smtp sender address is not configured")
	}
	switch settings.TLS {
	case TLSAuto, TLSStartTLS, TLSImplicit, TLSNone:
	default:
		return errors.New(errors.CodeValidation, http.StatusBadRequest, "smtp tls mode must be auto, starttls, tls or none")
	}
	if settings.Username != "" && settings.Password == "" {
		return errors.New(errors.CodeValidation, http.StatusBadRequest, "smtp username is set but password is empty")
	}
	if !strings.Contains(settings.FromEmail, "@") {
		return errors.New(errors.CodeValidation, http.StatusBadRequest, "smtp sender address is not a valid email address")
	}
	return nil
}

// Test implements Channel. It performs the EHLO handshake AI.md requires on
// every startup, plus authentication when credentials are configured.
func (c *SMTPChannel) Test(ctx context.Context) TestResult {
	c.mu.RLock()
	settings := c.settings
	c.mu.RUnlock()

	start := c.now()
	result := TestResult{}

	client, err := c.connect(ctx, settings)
	if err != nil {
		result.Latency = c.now().Sub(start)
		result.Detail = "could not connect to " + settings.Address()
		result.Err = err
		return result
	}
	defer func() { _ = client.Close() }()

	result.Connected = true
	if settings.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", settings.Username, settings.Password, settings.Host)); err != nil {
			result.Latency = c.now().Sub(start)
			result.Detail = "authentication rejected by " + settings.Address()
			result.Err = errors.Wrap(err, errors.CodeValidation, http.StatusBadRequest, "smtp authentication failed")
			return result
		}
		result.Authenticated = true
	}

	// A NOOP proves the session is usable end to end without generating a
	// message an operator did not ask for.
	if err := client.Noop(); err != nil {
		result.Latency = c.now().Sub(start)
		result.Detail = "session with " + settings.Address() + " did not stay usable"
		result.Err = errors.Wrap(err, errors.CodeUnavailable, http.StatusServiceUnavailable, "smtp session failed")
		return result
	}

	result.Delivered = true
	result.Latency = c.now().Sub(start)
	result.Detail = "connected to " + settings.Address()
	if settings.Detected {
		result.Detail += " (auto-detected)"
	}
	return result
}

// Send implements Channel.
func (c *SMTPChannel) Send(ctx context.Context, r Rendered) error {
	if r.Recipient.Email == "" {
		return errors.New(errors.CodeValidation, http.StatusBadRequest, "notification has no recipient address")
	}

	c.mu.RLock()
	settings := c.settings
	c.mu.RUnlock()

	if err := c.Validate(); err != nil {
		return err
	}

	sendCtx, cancel := context.WithTimeout(ctx, smtpSendTimeout)
	defer cancel()

	client, err := c.connect(sendCtx, settings)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if settings.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", settings.Username, settings.Password, settings.Host)); err != nil {
			return errors.Wrap(err, errors.CodeValidation, http.StatusBadRequest, "smtp authentication failed")
		}
	}
	if err := client.Mail(settings.FromEmail); err != nil {
		return errors.Wrap(err, errors.CodeUnavailable, http.StatusServiceUnavailable, "smtp sender rejected")
	}
	if err := client.Rcpt(r.Recipient.Email); err != nil {
		return errors.Wrap(err, errors.CodeUnavailable, http.StatusServiceUnavailable, "smtp recipient rejected")
	}

	writer, err := client.Data()
	if err != nil {
		return errors.Wrap(err, errors.CodeUnavailable, http.StatusServiceUnavailable, "smtp data command failed")
	}
	if _, err := writer.Write([]byte(BuildMessage(settings, r, c.now()))); err != nil {
		_ = writer.Close()
		return errors.Wrap(err, errors.CodeUnavailable, http.StatusServiceUnavailable, "smtp message write failed")
	}
	if err := writer.Close(); err != nil {
		return errors.Wrap(err, errors.CodeUnavailable, http.StatusServiceUnavailable, "smtp message was not accepted")
	}
	return client.Quit()
}

// connect dials the server and negotiates TLS according to the configured
// mode, returning a client that has already completed EHLO.
func (c *SMTPChannel) connect(ctx context.Context, settings SMTPSettings) (*smtp.Client, error) {
	address := settings.Address()
	conn, err := c.dial(ctx, "tcp", address)
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeUnavailable, http.StatusServiceUnavailable, "smtp connection failed")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	implicit := settings.TLS == TLSImplicit || (settings.TLS == TLSAuto && settings.Port == 465)
	if implicit {
		conn = tls.Client(conn, &tls.Config{ServerName: settings.Host, MinVersion: tls.VersionTLS12})
	}

	client, err := smtp.NewClient(conn, settings.Host)
	if err != nil {
		_ = conn.Close()
		return nil, errors.Wrap(err, errors.CodeUnavailable, http.StatusServiceUnavailable, "smtp handshake failed")
	}

	if !implicit && settings.TLS != TLSNone {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: settings.Host, MinVersion: tls.VersionTLS12}); err != nil {
				_ = client.Close()
				return nil, errors.Wrap(err, errors.CodeUnavailable, http.StatusServiceUnavailable, "smtp starttls failed")
			}
		} else if settings.TLS == TLSStartTLS {
			_ = client.Close()
			return nil, errors.New(errors.CodeUnavailable, http.StatusServiceUnavailable, "smtp server does not offer starttls")
		}
	}
	return client, nil
}

// ConfigSchema implements Channel. Every field carries the help text the
// admin panel shows behind its [?] control, and the environment variable
// that pre-fills it.
func (c *SMTPChannel) ConfigSchema() []Field {
	return []Field{
		{
			Name: "host", Label: "SMTP host", Kind: "text", Required: true,
			Placeholder: "auto-detect", EnvVar: "SMTP_HOST",
			Help:    "Hostname of the mail server that relays outbound email. Leave empty to sweep the local candidates on startup and use the first that answers an EHLO.",
			Example: "smtp.example.com",
		},
		{
			Name: "port", Label: "SMTP port", Kind: "number", Required: true,
			Placeholder: "587", EnvVar: "SMTP_PORT",
			Help:    "587 for STARTTLS submission, 465 for implicit TLS, 25 for a local relay. Auto-detection tries all three.",
			Example: "587",
		},
		{
			Name: "username", Label: "Username", Kind: "text", EnvVar: "SMTP_USERNAME",
			Help:    "SMTP AUTH user. Leave empty for a local relay that accepts unauthenticated mail from this host.",
			Example: "no-reply@example.com",
		},
		{
			Name: "password", Label: "Password", Kind: "password", Secret: true, EnvVar: "SMTP_PASSWORD",
			Help:     "SMTP AUTH password or provider app password. Leave the field empty when saving to keep the stored value.",
			Security: "Stored encrypted, never written to a log, and only ever returned masked.",
		},
		{
			Name: "tls", Label: "Encryption", Kind: "select", Required: true,
			Options: []string{TLSAuto, TLSStartTLS, TLSImplicit, TLSNone}, EnvVar: "SMTP_TLS",
			Help:     "auto picks implicit TLS on 465 and STARTTLS elsewhere. Choose none only for a relay on this machine.",
			Example:  TLSAuto,
			Security: "none sends credentials and message bodies in the clear; never use it across a network.",
		},
		{
			Name: "from_email", Label: "From address", Kind: "text", Required: true, EnvVar: "SMTP_FROM",
			Help:    "Envelope sender. Must be an address the mail server is allowed to send as, or the provider will reject the message.",
			Example: "no-reply@example.com",
		},
		{
			Name: "from_name", Label: "From name", Kind: "text", EnvVar: "SMTP_FROM_NAME",
			Help:    "Display name shown beside the sender address. Defaults to the application title.",
			Example: "cashp",
		},
		{
			Name: "reply_to", Label: "Reply-To", Kind: "text",
			Help:    "Address that replies go to. Usually the admin contact; leave empty to omit the header.",
			Example: "admin@example.com",
		},
	}
}

// Help implements Channel.
func (c *SMTPChannel) Help() Help {
	return Help{
		Summary: "Delivers email through any SMTP server. Enables itself as soon as a handshake succeeds.",
		Setup: []string{
			"Leave the host empty on first run: the server probes 127.0.0.1, the Docker bridge, the default gateway and the FQDN on ports 25, 465 and 587.",
			"To use a provider instead, enter its submission host and port, usually 587 with STARTTLS or 465 with implicit TLS.",
			"Enter the username and password the provider issued. Accounts with two-factor authentication need an app password, not the login password.",
			"Set the from address to an identity the provider is allowed to send as, or every message will bounce.",
			"Save, then press Test. A passing test enables email immediately; a failing test leaves the channel disabled.",
		},
		Troubleshooting: []HelpEntry{
			{Symptom: "connection refused", Resolution: "Nothing is listening on that host and port. Check the port and that outbound traffic to it is not firewalled."},
			{Symptom: "authentication failed", Resolution: "The provider rejected the credentials. Generate an app password if the account uses two-factor authentication."},
			{Symptom: "smtp server does not offer starttls", Resolution: "The server has no STARTTLS support. Switch to implicit TLS on 465, or to auto for a local relay."},
			{Symptom: "sender rejected", Resolution: "The from address is not one the server may send as. Use an address on a verified domain."},
			{Symptom: "messages send but never arrive", Resolution: "Publish SPF, DKIM and DMARC records for the sending domain; without them receivers silently discard mail."},
		},
		Comparison: Comparison{Speed: "seconds", Reliability: "high", RequiresAccount: false, Pricing: "free"},
	}
}

// DetectSMTP sweeps the candidate hosts in AI.md PART 18 priority order and
// returns the first host and port that completes an EHLO. All candidates
// failing is not an error: it simply means no relay is available and email
// stays disabled.
func DetectSMTP(ctx context.Context, hosts []string, dialer func(ctx context.Context, network, address string) (net.Conn, error)) (host string, port int, ok bool) {
	if dialer == nil {
		d := &net.Dialer{Timeout: smtpDialTimeout}
		dialer = d.DialContext
	}

	for _, candidate := range hosts {
		if candidate == "" {
			continue
		}
		for _, candidatePort := range smtpDetectPorts {
			if ctx.Err() != nil {
				return "", 0, false
			}
			if probeSMTP(ctx, dialer, candidate, candidatePort) {
				return candidate, candidatePort, true
			}
		}
	}
	return "", 0, false
}

// probeSMTP reports whether one host and port completes an SMTP greeting
// and EHLO within the probe timeout.
func probeSMTP(ctx context.Context, dialer func(ctx context.Context, network, address string) (net.Conn, error), host string, port int) bool {
	probeCtx, cancel := context.WithTimeout(ctx, smtpDialTimeout)
	defer cancel()

	conn, err := dialer(probeCtx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := probeCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	// Port 465 speaks TLS from the first byte. Certificate verification
	// stays on: a relay with an untrusted certificate simply is not
	// detected on 465 and is picked up on 25 or 587 instead.
	if port == 465 {
		conn = tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return false
	}
	defer func() { _ = client.Close() }()
	return client.Noop() == nil
}

// DetectionHosts builds the auto-detection candidate list in the priority
// order AI.md PART 18 defines. Empty inputs are skipped.
func DetectionHosts(gatewayIP, fqdn, globalIPv4 string) []string {
	hosts := []string{"127.0.0.1", "172.17.0.1", gatewayIP, fqdn, globalIPv4}
	if fqdn != "" {
		hosts = append(hosts, "mail."+fqdn, "smtp."+fqdn)
	}

	seen := map[string]struct{}{}
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		if host == "" {
			continue
		}
		if _, dup := seen[host]; dup {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	return out
}

// BuildMessage renders one RFC 5322 message. Header values are sanitised so
// a subject or display name can never inject an extra header.
func BuildMessage(settings SMTPSettings, r Rendered, now time.Time) string {
	from := settings.FromEmail
	if settings.FromName != "" {
		from = mime.QEncoding.Encode("utf-8", headerSafe(settings.FromName)) + " <" + settings.FromEmail + ">"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", headerSafe(r.Recipient.Email))
	if settings.ReplyTo != "" {
		fmt.Fprintf(&b, "Reply-To: %s\r\n", headerSafe(settings.ReplyTo))
	}
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", headerSafe(r.Subject)))
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	if r.ID != "" {
		fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", headerSafe(r.ID), headerSafe(hostPart(settings.FromEmail)))
	}
	if r.Event != "" {
		fmt.Fprintf(&b, "X-Notification-Event: %s\r\n", headerSafe(r.Event))
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(r.Body, "\r\n", "\n"), "\n", "\r\n"))
	return b.String()
}

// headerSafe strips the control characters that would let a value break out
// of its header and inject another one.
func headerSafe(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, value)
}

// hostPart returns the domain of an email address, or "localhost" when the
// address has none.
func hostPart(address string) string {
	if at := strings.LastIndex(address, "@"); at >= 0 && at < len(address)-1 {
		return address[at+1:]
	}
	return "localhost"
}
