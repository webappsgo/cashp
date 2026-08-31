package service

import (
	"strings"
	"testing"
)

func TestEvaluateEscalation(t *testing.T) {
	cases := []struct {
		name        string
		env         escalationEnv
		wantOK      bool
		wantReason  string
		reasonEmpty bool
	}{
		{
			name:        "already root",
			env:         escalationEnv{Elevated: true, User: "root"},
			wantOK:      true,
			reasonEmpty: true,
		},
		{
			name:        "wheel member with sudo",
			env:         escalationEnv{User: "alice", Groups: []string{"users", "wheel"}, HasSudo: true},
			wantOK:      true,
			reasonEmpty: true,
		},
		{
			name:        "sudo group member with sudo",
			env:         escalationEnv{User: "alice", Groups: []string{"sudo"}, HasSudo: true},
			wantOK:      true,
			reasonEmpty: true,
		},
		{
			name:        "group name case is ignored",
			env:         escalationEnv{User: "alice", Groups: []string{"WHEEL"}, HasSudo: true},
			wantOK:      true,
			reasonEmpty: true,
		},
		{
			name:        "sudo validates without a privileged group",
			env:         escalationEnv{User: "deploy", Groups: []string{"users"}, HasSudo: true, SudoValidated: true},
			wantOK:      true,
			reasonEmpty: true,
		},
		{
			name:        "doas permits the account",
			env:         escalationEnv{User: "alice", Groups: []string{"users"}, HasDoas: true, DoasPermits: true},
			wantOK:      true,
			reasonEmpty: true,
		},
		{
			name:        "pkexec with an admin group",
			env:         escalationEnv{User: "alice", Groups: []string{"admin"}, HasPkexec: true},
			wantOK:      true,
			reasonEmpty: true,
		},
		{
			name:       "no helper installed",
			env:        escalationEnv{User: "alice", Groups: []string{"users"}},
			wantOK:     false,
			wantReason: "no escalation helper (sudo, doas or pkexec) is installed on this host",
		},
		{
			name:       "helpers present but account unprivileged",
			env:        escalationEnv{User: "alice", Groups: []string{"users"}, HasSudo: true, HasPkexec: true},
			wantOK:     false,
			wantReason: `account "alice" is not in a privileged group (admin, root, sudo, wheel) and sudo/pkexec does not grant it root access`,
		},
		{
			name:       "doas installed but the account is denied",
			env:        escalationEnv{User: "alice", Groups: []string{"users"}, HasDoas: true},
			wantOK:     false,
			wantReason: `account "alice" is not in a privileged group (admin, root, sudo, wheel) and doas does not grant it root access`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := evaluateEscalation(tc.env)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (reason %q)", ok, tc.wantOK, reason)
			}
			if tc.reasonEmpty {
				if reason != "" {
					t.Errorf("reason = %q, want empty when escalation is possible", reason)
				}
				return
			}
			if reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}

func TestEvaluateWindowsEscalation(t *testing.T) {
	if ok, reason := evaluateWindowsEscalation(true, false, "SYSTEM"); !ok || reason != "" {
		t.Errorf("elevated token must report escalation possible, got %v %q", ok, reason)
	}
	if ok, reason := evaluateWindowsEscalation(false, true, "DESKTOP\\alice"); !ok || reason != "" {
		t.Errorf("Administrators member must report escalation possible, got %v %q", ok, reason)
	}
	ok, reason := evaluateWindowsEscalation(false, false, "DESKTOP\\bob")
	if ok {
		t.Fatal("a standard user must not report escalation possible")
	}
	if !strings.Contains(reason, `account "DESKTOP\bob"`) || !strings.Contains(reason, "Administrators") {
		t.Errorf("reason = %q, want it to name the account and the Administrators group", reason)
	}
}

func TestInPrivilegedGroup(t *testing.T) {
	cases := map[string]struct {
		groups []string
		want   bool
	}{
		"root":         {[]string{"root"}, true},
		"wheel":        {[]string{"users", "wheel"}, true},
		"sudo":         {[]string{"sudo"}, true},
		"admin":        {[]string{"admin"}, true},
		"mixed case":   {[]string{"Wheel"}, true},
		"unprivileged": {[]string{"users", "docker"}, false},
		"no groups":    {nil, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := (escalationEnv{Groups: tc.groups}).inPrivilegedGroup(); got != tc.want {
				t.Errorf("inPrivilegedGroup(%v) = %v, want %v", tc.groups, got, tc.want)
			}
		})
	}
}

func TestPrivilegedGroupListIsStable(t *testing.T) {
	if got := privilegedGroupList(); got != "admin, root, sudo, wheel" {
		t.Errorf("privilegedGroupList() = %q, want a sorted list", got)
	}
}

func TestAvailableHelpersOrder(t *testing.T) {
	env := escalationEnv{HasSudo: true, HasDoas: true, HasPkexec: true}
	got := strings.Join(availableHelpers(env), ",")
	if got != "sudo,doas,pkexec" {
		t.Errorf("availableHelpers = %q, want a stable sudo,doas,pkexec order", got)
	}
	if len(availableHelpers(escalationEnv{})) != 0 {
		t.Error("availableHelpers must be empty when no helper is installed")
	}
}

func TestParseDoasPermits(t *testing.T) {
	cases := []struct {
		name    string
		conf    string
		account string
		groups  []string
		want    bool
	}{
		{
			name:    "direct permit",
			conf:    "permit nopass alice\n",
			account: "alice",
			want:    true,
		},
		{
			name:    "group permit",
			conf:    "permit persist :wheel\n",
			account: "alice",
			groups:  []string{"users", "wheel"},
			want:    true,
		},
		{
			name:    "group permit is case insensitive",
			conf:    "permit :WHEEL\n",
			account: "alice",
			groups:  []string{"wheel"},
			want:    true,
		},
		{
			name:    "other account only",
			conf:    "permit nopass bob\n",
			account: "alice",
			want:    false,
		},
		{
			name:    "last matching rule wins",
			conf:    "permit :wheel\ndeny alice\n",
			account: "alice",
			groups:  []string{"wheel"},
			want:    false,
		},
		{
			name:    "deny then permit",
			conf:    "deny alice\npermit nopass alice\n",
			account: "alice",
			want:    true,
		},
		{
			name:    "comments and blank lines are ignored",
			conf:    "# permit alice\n\n   \npermit :wheel\n",
			account: "alice",
			groups:  []string{"wheel"},
			want:    true,
		},
		{
			name:    "setenv block is skipped before the identity",
			conf:    "permit nopass setenv { PATH=/usr/bin LANG=C } alice as root\n",
			account: "alice",
			want:    true,
		},
		{
			name:    "unrelated keywords are ignored",
			conf:    "unknown alice\n",
			account: "alice",
			want:    false,
		},
		{
			name:    "empty config permits nobody",
			conf:    "",
			account: "alice",
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseDoasPermits(tc.conf, tc.account, tc.groups); got != tc.want {
				t.Errorf("parseDoasPermits = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDoasIdentity(t *testing.T) {
	cases := []struct {
		name   string
		fields []string
		want   string
		wantOK bool
	}{
		{"plain", []string{"alice"}, "alice", true},
		{"options first", []string{"nopass", "keepenv", "persist", ":wheel"}, ":wheel", true},
		{"setenv block", []string{"setenv", "{", "PATH=/usr/bin", "}", "alice"}, "alice", true},
		{"setenv inline braces", []string{"setenv", "{PATH=/usr/bin}", "bob"}, "bob", true},
		{"options only", []string{"nopass", "persist"}, "", false},
		{"empty", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := doasIdentity(tc.fields)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("doasIdentity(%v) = (%q, %v), want (%q, %v)", tc.fields, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
