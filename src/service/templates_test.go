package service

import (
	"strings"
	"testing"
)

// testData returns a fixed TemplateData so rendering assertions never depend
// on the host the tests run on.
func testData(userMode bool) TemplateData {
	return TemplateData{
		Name:             "cashp",
		Org:              "webappsgo",
		DisplayName:      "cashp",
		Description:      "cashp service",
		DocumentationURL: "https://webappsgo.github.io/cashp",
		BinaryPath:       "/usr/local/bin/cashp",
		ConfigDir:        "/etc/webappsgo/cashp",
		DataDir:          "/var/lib/webappsgo/cashp",
		CacheDir:         "/var/cache/webappsgo/cashp",
		LogDir:           "/var/log/webappsgo/cashp",
		BackupDir:        "/mnt/Backups/webappsgo/cashp",
		RunDir:           "/var/run/webappsgo",
		PIDFile:          "/var/run/webappsgo/cashp.pid",
		PlistName:        "com.webappsgo.cashp",
		UserMode:         userMode,
	}
}

func TestRenderSystemdUnitSystemScope(t *testing.T) {
	unit, err := RenderSystemdUnit(testData(false))
	if err != nil {
		t.Fatalf("render systemd unit: %v", err)
	}
	want := []string{
		"Description=cashp service",
		"Documentation=https://webappsgo.github.io/cashp",
		"ExecStart=/usr/local/bin/cashp",
		"ExecReload=/bin/kill -HUP $MAINPID",
		"WorkingDirectory=/var/lib/webappsgo/cashp",
		"Restart=on-failure",
		"User=root",
		"Group=root",
		"WantedBy=multi-user.target",
		"# PERMANENT ROOT",
		"never",
		"drops privileges",
	}
	for _, fragment := range want {
		if !strings.Contains(unit, fragment) {
			t.Errorf("systemd unit missing %q\n%s", fragment, unit)
		}
	}
	if strings.Contains(unit, "User=cashp") {
		t.Error("systemd unit must not run the server as the cashp account: cashp requires permanent root")
	}
	if strings.Contains(unit, "ProtectSystem=strict") {
		t.Error("systemd unit must not enable ProtectSystem=strict: cashp manages host services")
	}
}

func TestRenderSystemdUnitUserScope(t *testing.T) {
	unit, err := RenderSystemdUnit(testData(true))
	if err != nil {
		t.Fatalf("render systemd user unit: %v", err)
	}
	if strings.Contains(unit, "User=root") {
		t.Error("user unit must not force User=root")
	}
	if !strings.Contains(unit, "WantedBy=default.target") {
		t.Errorf("user unit must install into default.target\n%s", unit)
	}
	if !strings.Contains(unit, "# USER SERVICE") {
		t.Errorf("user unit must explain its reduced capabilities\n%s", unit)
	}
}

func TestRenderOpenRCScript(t *testing.T) {
	script, err := RenderOpenRCScript(testData(false))
	if err != nil {
		t.Fatalf("render openrc script: %v", err)
	}
	want := []string{
		"#!/sbin/openrc-run",
		`name="cashp"`,
		`command="/usr/local/bin/cashp"`,
		`pidfile="/var/run/webappsgo/cashp.pid"`,
		`extra_started_commands="reload"`,
		"# PERMANENT ROOT",
		"start-stop-daemon --signal HUP",
	}
	for _, fragment := range want {
		if !strings.Contains(script, fragment) {
			t.Errorf("openrc script missing %q\n%s", fragment, script)
		}
	}
	if strings.Contains(script, "command_user=") {
		t.Error("openrc script must not set command_user: cashp requires permanent root")
	}
}

func TestRenderSysVInitScript(t *testing.T) {
	script, err := RenderSysVInitScript(testData(false))
	if err != nil {
		t.Fatalf("render sysvinit script: %v", err)
	}
	want := []string{
		"#!/bin/sh",
		"### BEGIN INIT INFO",
		"# Provides:",
		"NAME=cashp",
		"DAEMON=/usr/local/bin/cashp",
		"PIDFILE=/var/run/webappsgo/cashp.pid",
		"LOGFILE=/var/log/webappsgo/cashp/server.log",
		"do_reload()",
		"start|stop|restart|reload|status",
		"# PERMANENT ROOT",
	}
	for _, fragment := range want {
		if !strings.Contains(script, fragment) {
			t.Errorf("sysvinit script missing %q\n%s", fragment, script)
		}
	}
	if !strings.Contains(script, "# No --chuid and no su: the daemon stays root") {
		t.Error("sysvinit script must document that it never drops privileges")
	}
	if strings.Contains(script, "--chuid \"") || strings.Contains(script, "--chuid $") {
		t.Error("sysvinit script must not drop privileges with --chuid")
	}
}

func TestRenderRunitScripts(t *testing.T) {
	runScript, err := RenderRunitRun(testData(false))
	if err != nil {
		t.Fatalf("render runit run: %v", err)
	}
	if !strings.Contains(runScript, "exec /usr/local/bin/cashp 2>&1") {
		t.Errorf("runit run script must exec the binary\n%s", runScript)
	}
	if !strings.Contains(runScript, "# PERMANENT ROOT") {
		t.Errorf("runit run script must carry the permanent root notice\n%s", runScript)
	}

	logScript, err := RenderRunitLogRun(testData(false))
	if err != nil {
		t.Fatalf("render runit log run: %v", err)
	}
	if !strings.Contains(logScript, "exec svlogd -tt /var/log/webappsgo/cashp") {
		t.Errorf("runit log script must exec svlogd\n%s", logScript)
	}
}

func TestRenderRCDScript(t *testing.T) {
	script, err := RenderRCDScript(testData(false))
	if err != nil {
		t.Fatalf("render rc.d script: %v", err)
	}
	want := []string{
		"# PROVIDE: cashp",
		"# REQUIRE: NETWORKING",
		". /etc/rc.subr",
		`rcvar="cashp_enable"`,
		`: ${cashp_enable:="NO"}`,
		"run_rc_command \"$1\"",
		"# PERMANENT ROOT",
	}
	for _, fragment := range want {
		if !strings.Contains(script, fragment) {
			t.Errorf("rc.d script missing %q\n%s", fragment, script)
		}
	}
}

func TestRenderLaunchdPlist(t *testing.T) {
	plist, err := RenderLaunchdPlist(testData(false))
	if err != nil {
		t.Fatalf("render launchd plist: %v", err)
	}
	want := []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		"<string>com.webappsgo.cashp</string>",
		"<string>/usr/local/bin/cashp</string>",
		"<key>RunAtLoad</key>",
		"<string>/var/log/webappsgo/cashp/stdout.log</string>",
		"PERMANENT ROOT",
	}
	for _, fragment := range want {
		if !strings.Contains(plist, fragment) {
			t.Errorf("launchd plist missing %q\n%s", fragment, plist)
		}
	}
	if strings.Contains(plist, "<key>UserName</key>") {
		t.Error("launchd plist must not set UserName: cashp requires permanent root")
	}
}

func TestCommentLinesPrefixesEveryLine(t *testing.T) {
	got := commentLines("# ", "first\n\nthird")
	want := "# first\n#\n# third"
	if got != want {
		t.Errorf("commentLines = %q, want %q", got, want)
	}
}

func TestEveryUnixTemplateStatesPermanentRoot(t *testing.T) {
	renderers := map[string]func(TemplateData) (string, error){
		"systemd":  RenderSystemdUnit,
		"openrc":   RenderOpenRCScript,
		"sysvinit": RenderSysVInitScript,
		"runit":    RenderRunitRun,
		"rcd":      RenderRCDScript,
		"launchd":  RenderLaunchdPlist,
	}
	for name, renderer := range renderers {
		content, err := renderer(testData(false))
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		if !strings.Contains(content, "PERMANENT ROOT") {
			t.Errorf("%s definition does not state permanent root", name)
		}
		if !strings.Contains(content, "privilege drop after port binding is not possible") &&
			!strings.Contains(content, "not possible") {
			t.Errorf("%s definition does not explain why privilege drop is impossible", name)
		}
	}
}
