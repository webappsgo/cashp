//go:build linux

package service

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdManagerSystemScope(t *testing.T) {
	m := newSystemdManager(false)
	if m.Name() != "systemd" {
		t.Errorf("Name() = %q, want systemd", m.Name())
	}
	if m.unitPath != "/etc/systemd/system/cashp.service" {
		t.Errorf("unitPath = %q, want /etc/systemd/system/cashp.service", m.unitPath)
	}
	args := m.args([]string{"start", "cashp.service"})
	if strings.Join(args, " ") != "start cashp.service" {
		t.Errorf("args = %v, want no --user prefix for the system scope", args)
	}
	if m.data.UserMode {
		t.Error("system-scope template data must not be user mode")
	}
}

func TestSystemdManagerUserScope(t *testing.T) {
	m := newSystemdManager(true)
	if m.Name() != "systemd (user)" {
		t.Errorf("Name() = %q, want systemd (user)", m.Name())
	}
	wantSuffix := filepath.Join(".config", "systemd", "user", "cashp.service")
	if !strings.HasSuffix(m.unitPath, wantSuffix) {
		t.Errorf("unitPath = %q, want it to end in %q", m.unitPath, wantSuffix)
	}
	args := m.args([]string{"start", "cashp.service"})
	if strings.Join(args, " ") != "--user start cashp.service" {
		t.Errorf("args = %v, want a --user prefix for the user scope", args)
	}
	if !m.data.UserMode {
		t.Error("user-scope template data must be user mode")
	}
	// A user-scope operation must not demand elevation.
	if err := m.gate("starting the systemd service"); err != nil {
		t.Errorf("gate for a user unit = %v, want nil", err)
	}
}

func TestOpenRCAndSysVInitScriptPaths(t *testing.T) {
	openrc := newOpenRCManager()
	if openrc.Name() != "openrc" {
		t.Errorf("Name() = %q, want openrc", openrc.Name())
	}
	if openrc.scriptPath != "/etc/init.d/cashp" {
		t.Errorf("openrc scriptPath = %q, want /etc/init.d/cashp", openrc.scriptPath)
	}
	sysv := newSysVInitManager()
	if sysv.Name() != "sysvinit" {
		t.Errorf("Name() = %q, want sysvinit", sysv.Name())
	}
	if sysv.scriptPath != "/etc/init.d/cashp" {
		t.Errorf("sysvinit scriptPath = %q, want /etc/init.d/cashp", sysv.scriptPath)
	}
	if sysvRunlevelGlob != "/etc/rc[2-5].d/S*cashp" {
		t.Errorf("sysvRunlevelGlob = %q, want the rc2-5 start-link pattern", sysvRunlevelGlob)
	}
}

func TestRunitManagerLayout(t *testing.T) {
	m := newRunitManager()
	if m.Name() != "runit" {
		t.Errorf("Name() = %q, want runit", m.Name())
	}
	if m.defDir != "/etc/sv/cashp" {
		t.Errorf("defDir = %q, want /etc/sv/cashp", m.defDir)
	}
	known := map[string]bool{}
	for _, dir := range runitLinkDirs {
		known[dir] = true
	}
	if !known[m.linkDir] {
		t.Errorf("linkDir = %q, want one of %v", m.linkDir, runitLinkDirs)
	}
	if m.linkPath() != filepath.Join(m.linkDir, "cashp") {
		t.Errorf("linkPath() = %q, want the service name inside %q", m.linkPath(), m.linkDir)
	}
}

func TestParseRunitPID(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want int
	}{
		{"running", "run: cashp: (pid 1234) 5s", 1234},
		{"with log service", "run: cashp: (pid 900) 12s; run: log: (pid 901) 12s", 900},
		{"down", "down: cashp: 3s, normally up", 0},
		{"malformed", "run: cashp: (pid oops) 5s", 0},
		{"unterminated", "run: cashp: (pid 1234", 0},
		{"empty", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRunitPID(tc.out); got != tc.want {
				t.Errorf("parseRunitPID(%q) = %d, want %d", tc.out, got, tc.want)
			}
		})
	}
}
