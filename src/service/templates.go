package service

import (
	"strings"
	"text/template"
)

// permanentRootNotice is the mandatory exception statement embedded in every
// generated Unix service file (AI.md PART 25 "Service Templates" exception
// clause, IDEA.md "Security decisions & exceptions"). The comment prefix is
// supplied by each template because unit, rc and plist syntaxes differ.
const permanentRootNotice = `PERMANENT ROOT — cashp runs as root for its entire lifetime and never
drops privileges. The server manages libvirt/KVM virtual machines,
Docker/Incus/Podman containers, mail, DNS and firewall services on the
host, so a privilege drop after port binding is not possible: every
managed-service feature would stop working the moment privileges were
released. This is the documented exception in IDEA.md "Security decisions
& exceptions". Least privilege is enforced inside the application through
strict RBAC and per-tenant isolation instead of at the process level.`

// userModeNotice explains the unprivileged fallback service installed when
// the caller is not root (AI.md PART 24 "Service Installation Logic").
const userModeNotice = `USER SERVICE — this definition runs cashp as the invoking user. It can
only bind ports above 1024 and cannot manage host VMs, containers, mail,
DNS or the firewall, all of which require the permanent-root system
service. Install the system service as root for the full feature set.`

// commentLines prefixes every line of a multi-line notice with the comment
// marker of the target file format.
func commentLines(prefix, text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = strings.TrimRight(prefix, " ")
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// templateFuncs exposes the notice helpers to the service file templates.
var templateFuncs = template.FuncMap{
	"rootNotice": func(prefix string) string { return commentLines(prefix, permanentRootNotice) },
	"userNotice": func(prefix string) string { return commentLines(prefix, userModeNotice) },
}

// render executes one of the service file templates against d.
func render(name, text string, d TemplateData) (string, error) {
	tmpl, err := template.New(name).Funcs(templateFuncs).Parse(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, d); err != nil {
		return "", err
	}
	return b.String(), nil
}

const systemdTemplate = `[Unit]
Description={{.Description}}
Documentation={{.DocumentationURL}}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{.BinaryPath}}
ExecReload=/bin/kill -HUP $MAINPID
WorkingDirectory={{.DataDir}}
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
{{if .UserMode}}
{{userNotice "# "}}
{{else}}
{{rootNotice "# "}}
User=root
Group=root

# The generic template's filesystem sandboxing directives (strict
# ProtectSystem, ProtectHome, PrivateTmp) are intentionally disabled below
# for cashp: the server must read and write host service configuration,
# hypervisor and container sockets, and tenant directories that live
# outside its own state directories. Restricting them would break the
# managed-service features described above.
ProtectSystem=off
ProtectHome=no
PrivateTmp=no
NoNewPrivileges=no
{{end}}
[Install]
WantedBy={{if .UserMode}}default.target{{else}}multi-user.target{{end}}
`

// RenderSystemdUnit renders the systemd unit file for d.
func RenderSystemdUnit(d TemplateData) (string, error) {
	return render("systemd", systemdTemplate, d)
}

const openrcTemplate = `#!/sbin/openrc-run
# Service identity comes from {{.Name}} so config and data paths stay stable
# across binary renames (AI.md PART 0).
#
{{rootNotice "# "}}

name="{{.Name}}"
description="{{.Description}}"
command="{{.BinaryPath}}"
command_args=""
pidfile="{{.PIDFile}}"
command_background=true
output_log="{{.LogDir}}/server.log"
error_log="{{.LogDir}}/error.log"

# No command_user is set on purpose: see the permanent root note above.

extra_started_commands="reload"

depend() {
    need net
    after firewall
    use dns logger
}

start_pre() {
    checkpath -d -m 0755 "{{.RunDir}}"
    checkpath -d -m 0750 "{{.LogDir}}"
    checkpath -d -m 0750 "{{.DataDir}}"
}

reload() {
    ebegin "Reloading {{.Name}}"
    start-stop-daemon --signal HUP --pidfile "${pidfile}"
    eend $?
}
`

// RenderOpenRCScript renders the OpenRC init script for d.
func RenderOpenRCScript(d TemplateData) (string, error) {
	return render("openrc", openrcTemplate, d)
}

const sysvinitTemplate = `#!/bin/sh
### BEGIN INIT INFO
# Provides:          {{.Name}}
# Required-Start:    $network $remote_fs $syslog
# Required-Stop:     $network $remote_fs $syslog
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: {{.DisplayName}}
# Description:       {{.Description}}
### END INIT INFO
#
{{rootNotice "# "}}

NAME={{.Name}}
DAEMON={{.BinaryPath}}
PIDFILE={{.PIDFile}}
LOGFILE={{.LogDir}}/server.log

is_running() {
    [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null
}

do_start() {
    if is_running; then
        echo "$NAME is already running (pid $(cat "$PIDFILE"))"
        return 0
    fi
    echo "Starting $NAME..."
    mkdir -p "$(dirname "$PIDFILE")" "$(dirname "$LOGFILE")"
    # No --chuid and no su: the daemon stays root, see the note above.
    if command -v start-stop-daemon >/dev/null 2>&1; then
        start-stop-daemon --start --quiet --background --make-pidfile \
            --pidfile "$PIDFILE" --exec "$DAEMON" --no-close >> "$LOGFILE" 2>&1
    else
        "$DAEMON" >> "$LOGFILE" 2>&1 &
        echo $! > "$PIDFILE"
    fi
    return $?
}

do_stop() {
    if ! is_running; then
        echo "$NAME is not running"
        rm -f "$PIDFILE"
        return 0
    fi
    echo "Stopping $NAME..."
    if command -v start-stop-daemon >/dev/null 2>&1; then
        start-stop-daemon --stop --quiet --pidfile "$PIDFILE" --retry 30
    else
        kill "$(cat "$PIDFILE")" 2>/dev/null
    fi
    rm -f "$PIDFILE"
    return 0
}

do_reload() {
    if ! is_running; then
        echo "$NAME is not running"
        return 3
    fi
    kill -HUP "$(cat "$PIDFILE")" 2>/dev/null
    echo "Reloaded $NAME"
    return 0
}

do_status() {
    if is_running; then
        echo "$NAME is running (pid $(cat "$PIDFILE"))"
        return 0
    fi
    echo "$NAME is stopped"
    return 3
}

case "$1" in
    start)
        do_start
        ;;
    stop)
        do_stop
        ;;
    restart)
        do_stop
        sleep 1
        do_start
        ;;
    reload)
        do_reload
        ;;
    status)
        do_status
        ;;
    *)
        echo "Usage: $0 {start|stop|restart|reload|status}"
        exit 1
        ;;
esac
exit $?
`

// RenderSysVInitScript renders the SysVinit init script for d.
func RenderSysVInitScript(d TemplateData) (string, error) {
	return render("sysvinit", sysvinitTemplate, d)
}

const runitRunTemplate = `#!/bin/sh
{{rootNotice "# "}}

exec {{.BinaryPath}} 2>&1
`

// RenderRunitRun renders the runit run script for d.
func RenderRunitRun(d TemplateData) (string, error) {
	return render("runit-run", runitRunTemplate, d)
}

const runitLogRunTemplate = `#!/bin/sh
# svlogd writes the rotated service log into the {{.Name}} log directory.

exec svlogd -tt {{.LogDir}}
`

// RenderRunitLogRun renders the runit log/run script for d.
func RenderRunitLogRun(d TemplateData) (string, error) {
	return render("runit-log-run", runitLogRunTemplate, d)
}

const rcdTemplate = `#!/bin/sh

# PROVIDE: {{.Name}}
# REQUIRE: NETWORKING
# KEYWORD: shutdown
#
{{rootNotice "# "}}

. /etc/rc.subr

name="{{.Name}}"
rcvar="{{.Name}}_enable"
command="{{.BinaryPath}}"
pidfile="{{.PIDFile}}"
command_args=""
extra_commands="reload"
reload_cmd="{{.Name}}_reload"

{{.Name}}_reload() {
    if [ -f "${pidfile}" ]; then
        kill -HUP "$(cat "${pidfile}")"
        echo "Reloaded {{.Name}}"
    else
        echo "{{.Name}} is not running"
        return 1
    fi
}

load_rc_config $name
: {{"${"}}{{.Name}}_enable:="NO"}
run_rc_command "$1"
`

// RenderRCDScript renders the FreeBSD rc.d script for d.
func RenderRCDScript(d TemplateData) (string, error) {
	return render("rcd", rcdTemplate, d)
}

const launchdTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.PlistName}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath}}</string>
    </array>
{{if .UserMode}}
    <!--
{{userNotice "    "}}
    -->
{{else}}
    <!--
{{rootNotice "    "}}
    No UserName or GroupName key is set on purpose: launchd starts the
    daemon as root and it stays root.
    -->
{{end}}
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>WorkingDirectory</key>
    <string>{{.DataDir}}</string>
    <key>StandardOutPath</key>
    <string>{{.LogDir}}/stdout.log</string>
    <key>StandardErrorPath</key>
    <string>{{.LogDir}}/stderr.log</string>
</dict>
</plist>
`

// RenderLaunchdPlist renders the launchd property list for d.
func RenderLaunchdPlist(d TemplateData) (string, error) {
	return render("launchd", launchdTemplate, d)
}
