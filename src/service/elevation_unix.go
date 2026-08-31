//go:build !windows

package service

import (
	"context"
	"os"
	"os/user"
	"strconv"
	"time"
)

// doasConfPath is the doas(1) rule file consulted when deciding whether the
// current account may escalate.
const doasConfPath = "/etc/doas.conf"

// sudoValidateTimeout bounds the non-interactive `sudo -n -v` probe so a
// misconfigured sudoers file cannot hang the CLI.
const sudoValidateTimeout = 5 * time.Second

// IsElevated reports whether the process runs with root privileges.
func IsElevated() bool {
	return os.Geteuid() == 0
}

// CanEscalate reports whether the current account is actually able to gain
// root privileges. It never prompts for a password: the probes it runs are
// strictly non-interactive. When escalation is impossible the second value
// explains why, so callers show an informative error rather than a prompt
// the user cannot answer (AI.md PART 24 "Overview").
func CanEscalate() (bool, string) {
	return evaluateEscalation(currentEscalationEnv())
}

// currentEscalationEnv probes the host for everything evaluateEscalation
// needs.
func currentEscalationEnv() escalationEnv {
	env := escalationEnv{
		Elevated:  IsElevated(),
		User:      currentUserName(),
		Groups:    currentGroupNames(),
		HasSudo:   hasBinary("sudo"),
		HasDoas:   hasBinary("doas"),
		HasPkexec: hasBinary("pkexec"),
	}
	if env.Elevated {
		return env
	}
	if env.HasSudo {
		env.SudoValidated = sudoValidates()
	}
	if env.HasDoas {
		if conf, err := os.ReadFile(doasConfPath); err == nil {
			env.DoasPermits = parseDoasPermits(string(conf), env.User, env.Groups)
		}
	}
	return env
}

// sudoValidates runs the non-interactive sudo credential check. It returns
// true only when sudo would run without asking for a password.
func sudoValidates() bool {
	ctx, cancel := context.WithTimeout(context.Background(), sudoValidateTimeout)
	defer cancel()
	return run(ctx, "sudo", "-n", "-v") == nil
}

// currentUserName returns the login name of the current account, falling
// back to the numeric UID when the account has no passwd entry.
func currentUserName() string {
	u, err := user.Current()
	if err != nil {
		return "uid " + strconv.Itoa(os.Getuid())
	}
	return u.Username
}

// currentGroupNames resolves the group names the current account belongs
// to. Unresolvable group IDs are skipped rather than failing the probe.
func currentGroupNames() []string {
	u, err := user.Current()
	if err != nil {
		return nil
	}
	ids, err := u.GroupIds()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		g, err := user.LookupGroupId(id)
		if err != nil {
			continue
		}
		names = append(names, g.Name)
	}
	return names
}
