package service

import (
	"fmt"
	"sort"
	"strings"
)

// privilegedGroups are the group names that grant a user the ability to
// escalate on Linux, BSD and macOS hosts (AI.md PART 24 "Escalation
// Detection by OS").
var privilegedGroups = map[string]bool{
	"root":  true,
	"wheel": true,
	"sudo":  true,
	"admin": true,
}

// escalationEnv is the observed escalation environment of the current
// process. Keeping it a plain struct lets the decision logic be unit-tested
// without probing the host.
type escalationEnv struct {
	// Elevated reports whether the process already runs as root/Administrator.
	Elevated bool
	// User is the current account name.
	User string
	// Groups are the group names the current account belongs to.
	Groups []string
	// HasSudo, HasDoas and HasPkexec report which escalation helpers exist.
	HasSudo   bool
	HasDoas   bool
	HasPkexec bool
	// SudoValidated reports that `sudo -n -v` succeeded, meaning sudo can be
	// used without any password prompt.
	SudoValidated bool
	// DoasPermits reports that /etc/doas.conf grants this user or one of its
	// groups.
	DoasPermits bool
}

// inPrivilegedGroup reports whether any of the account's groups grants
// escalation rights.
func (e escalationEnv) inPrivilegedGroup() bool {
	for _, g := range e.Groups {
		if privilegedGroups[strings.ToLower(g)] {
			return true
		}
	}
	return false
}

// evaluateEscalation decides whether the account behind env can actually
// escalate. When it cannot, the second return value explains why, so the
// caller can print an informative error instead of prompting for a password
// the user could never satisfy.
func evaluateEscalation(env escalationEnv) (bool, string) {
	if env.Elevated {
		return true, ""
	}
	privileged := env.inPrivilegedGroup()
	if env.HasSudo && (privileged || env.SudoValidated) {
		return true, ""
	}
	if env.HasDoas && env.DoasPermits {
		return true, ""
	}
	if env.HasPkexec && privileged {
		return true, ""
	}
	helpers := availableHelpers(env)
	if len(helpers) == 0 {
		return false, "no escalation helper (sudo, doas or pkexec) is installed on this host"
	}
	return false, fmt.Sprintf(
		"account %q is not in a privileged group (%s) and %s does not grant it root access",
		env.User, privilegedGroupList(), strings.Join(helpers, "/"),
	)
}

// availableHelpers lists the escalation helpers present on the host, in a
// stable order.
func availableHelpers(env escalationEnv) []string {
	var helpers []string
	if env.HasSudo {
		helpers = append(helpers, "sudo")
	}
	if env.HasDoas {
		helpers = append(helpers, "doas")
	}
	if env.HasPkexec {
		helpers = append(helpers, "pkexec")
	}
	return helpers
}

// privilegedGroupList renders the privileged group names in a stable,
// comma-separated order for error messages.
func privilegedGroupList() string {
	names := make([]string, 0, len(privilegedGroups))
	for name := range privilegedGroups {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// evaluateWindowsEscalation decides whether a non-elevated Windows account
// can raise itself through UAC or runas.
func evaluateWindowsEscalation(elevated, inAdministrators bool, account string) (bool, string) {
	if elevated {
		return true, ""
	}
	if inAdministrators {
		return true, ""
	}
	return false, fmt.Sprintf(
		// Plain double quotes, not %q: %q would escape the backslash in a
		// DOMAIN\user account name, which no caller expects.
		`account "%s" is not a member of the local Administrators group, so Windows cannot elevate it through UAC or runas`,
		account,
	)
}

// parseDoasPermits reports whether a doas.conf grants root access to the
// given account or one of its groups. Both "permit" and "deny" rules are
// evaluated and the last matching rule wins, following doas(1) semantics.
func parseDoasPermits(conf, account string, groups []string) bool {
	groupSet := make(map[string]bool, len(groups))
	for _, g := range groups {
		groupSet[strings.ToLower(g)] = true
	}
	permitted := false
	for _, raw := range strings.Split(conf, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		action := fields[0]
		if action != "permit" && action != "deny" {
			continue
		}
		identity, ok := doasIdentity(fields[1:])
		if !ok {
			continue
		}
		matched := false
		if strings.HasPrefix(identity, ":") {
			matched = groupSet[strings.ToLower(strings.TrimPrefix(identity, ":"))]
		} else {
			matched = identity == account
		}
		if matched {
			permitted = action == "permit"
		}
	}
	return permitted
}

// doasOptions are the rule options that may precede the identity field of a
// doas.conf rule.
var doasOptions = map[string]bool{
	"nopass":    true,
	"persist":   true,
	"nopersist": true,
	"keepenv":   true,
}

// doasIdentity extracts the identity field of a doas.conf rule, skipping
// leading options including a braced setenv block.
func doasIdentity(fields []string) (string, bool) {
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if doasOptions[field] {
			continue
		}
		if field == "setenv" {
			depth := 0
			for i+1 < len(fields) {
				i++
				depth += strings.Count(fields[i], "{") - strings.Count(fields[i], "}")
				if depth <= 0 {
					break
				}
			}
			continue
		}
		return field, true
	}
	return "", false
}
