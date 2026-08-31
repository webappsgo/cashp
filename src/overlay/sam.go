package overlay

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// i2pBase64 is I2P's base64 alphabet: standard base64 with '+' and '/'
// replaced by '-' and '~'. SAM returns destinations in this encoding.
var i2pBase64 = base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-~")

// samSession is a live SAMv3 control connection (Model B). The connection
// stays open for the lifetime of the eepsite: closing it tears the session,
// and therefore the eepsite, down.
type samSession struct {
	conn   net.Conn
	reader *bufio.Reader

	// writeMu serializes commands written to the control connection.
	writeMu sync.Mutex

	// stateMu guards failure and closed.
	stateMu sync.Mutex
	failure error
	closed  bool

	// dead is closed as soon as the session is known to be lost.
	dead chan struct{}
}

// startSAMEepsite creates the eepsite through an external SAMv3 bridge: it
// loads (or generates and persists) the destination, opens a STREAM session
// bound to it and asks the router to forward incoming eepsite connections to
// the dedicated loopback backend port.
func startSAMEepsite(ctx context.Context, cfg I2PConfig, paths i2pPaths, backendPort int, rt *i2pRuntime) error {
	dialer := &net.Dialer{Timeout: samDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", cfg.SAMAddress)
	if err != nil {
		return fmt.Errorf("dial SAM %s: %w", cfg.SAMAddress, err)
	}

	session := &samSession{
		conn:   conn,
		reader: bufio.NewReader(conn),
		dead:   make(chan struct{}),
	}

	// Setup is bounded by the bootstrap timeout; the deadline is cleared
	// again before the session is handed to the keepalive loop.
	if err := conn.SetDeadline(time.Now().Add(time.Duration(cfg.BootstrapTimeout) * time.Second)); err != nil {
		conn.Close()
		return fmt.Errorf("set SAM deadline: %w", err)
	}

	address, err := session.establish(cfg, paths.keys, backendPort)
	if err != nil {
		conn.Close()
		return err
	}

	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return fmt.Errorf("clear SAM deadline: %w", err)
	}
	go session.keepalive()

	rt.sam = session
	rt.eepsite = address
	return nil
}

// establish runs the full SAMv3 handshake and returns the .b32.i2p address
// of the session's destination.
func (s *samSession) establish(cfg I2PConfig, keysPath string, backendPort int) (string, error) {
	if err := s.send("HELLO VERSION MIN=3.0 MAX=3.3"); err != nil {
		return "", err
	}
	if _, err := s.readReply("HELLO REPLY"); err != nil {
		return "", err
	}

	private, err := s.loadOrCreateDestination(keysPath, cfg.SignatureType)
	if err != nil {
		return "", err
	}

	if err := s.send("SESSION CREATE STYLE=STREAM ID=%s DESTINATION=%s "+
		"inbound.length=%d outbound.length=%d inbound.quantity=%d outbound.quantity=%d",
		samSessionID, private,
		cfg.InboundLength, cfg.OutboundLength,
		cfg.InboundQuantity, cfg.OutboundQuantity); err != nil {
		return "", err
	}
	if _, err := s.readReply("SESSION STATUS"); err != nil {
		return "", err
	}

	// NAMING LOOKUP NAME=ME returns this session's public destination, the
	// value the .b32.i2p address is derived from.
	if err := s.send("NAMING LOOKUP NAME=ME"); err != nil {
		return "", err
	}
	naming, err := s.readReply("NAMING REPLY")
	if err != nil {
		return "", err
	}
	public, ok := naming["VALUE"]
	if !ok {
		return "", errors.New("SAM NAMING REPLY carried no destination")
	}
	destination, err := i2pBase64.DecodeString(public)
	if err != nil {
		return "", fmt.Errorf("decode SAM destination: %w", err)
	}

	// SILENT=true keeps the router from prefixing each forwarded connection
	// with the peer destination, so the backend listener receives plain
	// HTTP with nothing to strip.
	if err := s.send("STREAM FORWARD ID=%s PORT=%d HOST=127.0.0.1 SILENT=true", samSessionID, backendPort); err != nil {
		return "", err
	}
	if _, err := s.readReply("STREAM STATUS"); err != nil {
		return "", err
	}

	return B32Address(destination), nil
}

// loadOrCreateDestination returns the persisted private destination, asking
// the router to generate and persisting a new one on first run. The key file
// is what makes the .b32.i2p address stable across restarts.
func (s *samSession) loadOrCreateDestination(keysPath string, signatureType int) (string, error) {
	if data, err := os.ReadFile(keysPath); err == nil {
		if key := strings.TrimSpace(string(data)); key != "" {
			return key, nil
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read i2p destination %s: %w", keysPath, err)
	}

	if err := s.send("DEST GENERATE SIGNATURE_TYPE=%d", signatureType); err != nil {
		return "", err
	}
	reply, err := s.readReply("DEST REPLY")
	if err != nil {
		return "", err
	}
	private, ok := reply["PRIV"]
	if !ok {
		return "", errors.New("SAM DEST REPLY carried no private destination")
	}
	if err := writeSecretFile(keysPath, []byte(private)); err != nil {
		return "", fmt.Errorf("persist i2p destination: %w", err)
	}
	return private, nil
}

// send writes one SAM command line.
func (s *samSession) send(format string, args ...any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if _, err := fmt.Fprintf(s.conn, format+"\n", args...); err != nil {
		return fmt.Errorf("write SAM command: %w", err)
	}
	return nil
}

// readReply reads one SAM reply line, verifies it starts with the expected
// two-word prefix and returns its key/value pairs. A RESULT other than OK is
// reported as an error carrying the router's message.
func (s *samSession) readReply(prefix string) (map[string]string, error) {
	line, err := s.reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read SAM reply %q: %w", prefix, err)
	}
	line = strings.TrimRight(line, "\r\n")

	if !strings.HasPrefix(line, prefix) {
		return nil, fmt.Errorf("unexpected SAM reply, wanted %q: %s", prefix, line)
	}
	fields := parseSAMFields(strings.TrimSpace(strings.TrimPrefix(line, prefix)))

	if result, ok := fields["RESULT"]; ok && result != "OK" {
		message := fields["MESSAGE"]
		if message == "" {
			message = result
		}
		return nil, fmt.Errorf("SAM %s failed: %s", prefix, message)
	}
	return fields, nil
}

// parseSAMFields splits a SAM reply tail into KEY=VALUE pairs, honouring
// double-quoted values such as MESSAGE="tunnel build failed".
func parseSAMFields(tail string) map[string]string {
	fields := make(map[string]string)
	for _, token := range splitSAMTokens(tail) {
		key, value, found := strings.Cut(token, "=")
		if !found {
			continue
		}
		value = strings.TrimSuffix(strings.TrimPrefix(value, `"`), `"`)
		fields[key] = value
	}
	return fields
}

// splitSAMTokens splits on spaces except inside double quotes.
func splitSAMTokens(tail string) []string {
	var tokens []string
	var current strings.Builder
	quoted := false

	for _, r := range tail {
		switch {
		case r == '"':
			quoted = !quoted
			current.WriteRune(r)
		case r == ' ' && !quoted:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// keepalive answers the router's PING keepalives and marks the session dead
// as soon as the control connection reports anything else or fails, which is
// what the scheduler's health task observes.
func (s *samSession) keepalive() {
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			s.fail(fmt.Errorf("SAM control connection lost: %w", err))
			return
		}
		line = strings.TrimRight(line, "\r\n")

		switch {
		case strings.HasPrefix(line, "PING"):
			token := strings.TrimSpace(strings.TrimPrefix(line, "PING"))
			if err := s.send("PONG %s", token); err != nil {
				s.fail(err)
				return
			}
		case line == "":
			continue
		default:
			// Any unsolicited status on a forwarding session means the
			// router tore it down.
			s.fail(fmt.Errorf("SAM session ended: %s", line))
			return
		}
	}
}

// fail records the first failure and wakes every health check.
func (s *samSession) fail(err error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	if s.closed || s.failure != nil {
		return
	}
	s.failure = err
	close(s.dead)
}

// health reports the session failure, if any.
func (s *samSession) health() error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	if s.failure != nil {
		return s.failure
	}
	if s.closed {
		return ErrNotStarted
	}
	return nil
}

// close tears the SAM session down. A shutdown-induced read error in the
// keepalive loop is not reported as a failure.
func (s *samSession) close() error {
	s.stateMu.Lock()
	alreadyClosed := s.closed
	s.closed = true
	s.stateMu.Unlock()

	if alreadyClosed {
		return nil
	}
	return s.conn.Close()
}
