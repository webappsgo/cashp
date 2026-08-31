package hosting

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os/exec"
	"strings"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// defaultCommandTimeout bounds every host command. A config check or reload
// that hangs must never hold a request open.
const defaultCommandTimeout = 30 * time.Second

// ErrCommandFailed is the sentinel wrapped by a Runner when a command exits
// non-zero. Callers surface a typed API error instead of the raw output.
var ErrCommandFailed = errors.New("hosting: command failed")

// ExecRunner runs a host command directly, never through a shell. The
// environment is reset to a fixed minimal set so nothing a tenant controls
// can influence how the binary resolves or behaves.
type ExecRunner struct {
	// Timeout bounds a single command; zero uses defaultCommandTimeout.
	Timeout time.Duration
	// Env replaces the fixed minimal environment when non-empty.
	Env []string
}

// Run executes name with args and returns the combined output.
func (r ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("hosting: empty command")
	}
	if strings.ContainsRune(name, 0) {
		return nil, errors.New("hosting: command contains a null byte")
	}
	for _, a := range args {
		if strings.ContainsRune(a, 0) {
			return nil, errors.New("hosting: argument contains a null byte")
		}
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	if len(r.Env) > 0 {
		cmd.Env = append([]string(nil), r.Env...)
	} else {
		cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, errors.Join(ErrCommandFailed, err)
	}
	return out, nil
}

// CommandSet holds the argv prefixes for every host command the package runs.
// Each entry is a full argv slice; per-call arguments are appended, never
// interpolated, so no value can add a flag or a shell token.
type CommandSet struct {
	// NginxCheck validates the whole nginx configuration tree.
	NginxCheck []string
	// NginxReload asks a running nginx to re-read its configuration.
	NginxReload []string
	// NamedCheckConf validates the generated BIND zone include file.
	NamedCheckConf []string
	// NamedCheckZone validates one zone file; zone name and path are appended.
	NamedCheckZone []string
	// NamedReconfig makes BIND pick up added or removed zones.
	NamedReconfig []string
	// NamedReload reloads a single zone; the zone name is appended.
	NamedReload []string
	// PostfixCheck validates the Postfix configuration.
	PostfixCheck []string
	// PostfixReload reloads Postfix.
	PostfixReload []string
	// DovecotCheck validates the Dovecot configuration.
	DovecotCheck []string
	// DovecotReload reloads Dovecot.
	DovecotReload []string
	// OpenDKIMReload reloads OpenDKIM after a key table change.
	OpenDKIMReload []string
	// GitClone fetches a PaaS release tree; branch, remote, and destination
	// are appended as separate argv entries.
	GitClone []string
}

// DefaultCommands returns the command set for the native services IDEA.md
// names: nginx, BIND, Postfix, Dovecot, and OpenDKIM.
func DefaultCommands() CommandSet {
	return CommandSet{
		NginxCheck:     []string{"nginx", "-t"},
		NginxReload:    []string{"nginx", "-s", "reload"},
		NamedCheckConf: []string{"named-checkconf"},
		NamedCheckZone: []string{"named-checkzone"},
		NamedReconfig:  []string{"rndc", "reconfig"},
		NamedReload:    []string{"rndc", "reload"},
		PostfixCheck:   []string{"postfix", "check"},
		PostfixReload:  []string{"postfix", "reload"},
		DovecotCheck:   []string{"doveconf", "-n"},
		DovecotReload:  []string{"doveadm", "reload"},
		OpenDKIMReload: []string{"systemctl", "reload-or-restart", "opendkim"},
		GitClone:       []string{"git", "clone", "--depth", "1", "--no-tags", "--single-branch", "--branch"},
	}
}

// withDefaults fills any command the operator did not override.
func (c CommandSet) withDefaults() CommandSet {
	d := DefaultCommands()
	fill := func(v, fallback []string) []string {
		if len(v) == 0 {
			return fallback
		}
		return v
	}
	return CommandSet{
		NginxCheck:     fill(c.NginxCheck, d.NginxCheck),
		NginxReload:    fill(c.NginxReload, d.NginxReload),
		NamedCheckConf: fill(c.NamedCheckConf, d.NamedCheckConf),
		NamedCheckZone: fill(c.NamedCheckZone, d.NamedCheckZone),
		NamedReconfig:  fill(c.NamedReconfig, d.NamedReconfig),
		NamedReload:    fill(c.NamedReload, d.NamedReload),
		PostfixCheck:   fill(c.PostfixCheck, d.PostfixCheck),
		PostfixReload:  fill(c.PostfixReload, d.PostfixReload),
		DovecotCheck:   fill(c.DovecotCheck, d.DovecotCheck),
		DovecotReload:  fill(c.DovecotReload, d.DovecotReload),
		OpenDKIMReload: fill(c.OpenDKIMReload, d.OpenDKIMReload),
		GitClone:       fill(c.GitClone, d.GitClone),
	}
}

// run executes an argv prefix with extra arguments appended.
func (s *Service) run(ctx context.Context, argv []string, extra ...string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, apperr.New(apperr.CodeInternal, 500, "service command is not configured")
	}
	args := append(append([]string(nil), argv[1:]...), extra...)
	return s.runner.Run(ctx, argv[0], args...)
}

// randomID returns a 32-character hex identifier.
func randomID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(buf)
}
