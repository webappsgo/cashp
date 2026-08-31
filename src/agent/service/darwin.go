//go:build darwin

package service

import (
	"context"
	"encoding/xml"
	"path/filepath"
	"strings"
)

// launchDaemonDir is where system-wide launchd jobs live.
const launchDaemonDir = "/Library/LaunchDaemons"

// launchLabel is the reverse-DNS job label launchd identifies the agent by.
var launchLabel = "com." + strings.ToLower(UnitName)

// Detect returns the launchd manager, the only supported option on macOS.
func Detect() (Manager, error) {
	if !hasBinary("launchctl") {
		return nil, ErrUnsupportedPlatform
	}
	return &launchdManager{}, nil
}

// launchdManager drives the agent job through launchctl.
type launchdManager struct{}

// Name identifies the init system.
func (m *launchdManager) Name() string { return "launchd" }

// plistPath is the generated job definition location.
func (m *launchdManager) plistPath() string {
	return filepath.Join(launchDaemonDir, launchLabel+".plist")
}

// Install writes the job definition and loads it.
func (m *launchdManager) Install(ctx context.Context, opts Options) error {
	if err := writeUnit(m.plistPath(), m.plist(opts)); err != nil {
		return err
	}
	return runCommand(ctx, "launchctl", "load", "-w", m.plistPath())
}

// Uninstall unloads the job and removes its definition.
func (m *launchdManager) Uninstall(ctx context.Context) error {
	if !fileExists(m.plistPath()) {
		return ErrNotInstalled
	}

	// An already-unloaded job makes this call fail, which must not block
	// removing the definition.
	_ = runCommand(ctx, "launchctl", "unload", "-w", m.plistPath())
	return removeUnit(m.plistPath())
}

// Start starts the job.
func (m *launchdManager) Start(ctx context.Context) error {
	if !fileExists(m.plistPath()) {
		return ErrNotInstalled
	}
	return runCommand(ctx, "launchctl", "start", launchLabel)
}

// Stop stops the job.
func (m *launchdManager) Stop(ctx context.Context) error {
	if !fileExists(m.plistPath()) {
		return ErrNotInstalled
	}
	return runCommand(ctx, "launchctl", "stop", launchLabel)
}

// Restart stops and starts the job: launchd has no single restart verb.
func (m *launchdManager) Restart(ctx context.Context) error {
	if !fileExists(m.plistPath()) {
		return ErrNotInstalled
	}

	// A job that is not currently running makes the stop fail, which must
	// not prevent starting it.
	_ = runCommand(ctx, "launchctl", "stop", launchLabel)
	return runCommand(ctx, "launchctl", "start", launchLabel)
}

// Status reports installation and runtime state.
func (m *launchdManager) Status(ctx context.Context) (Status, error) {
	status := Status{Manager: m.Name(), State: StateNotFound}
	if !fileExists(m.plistPath()) {
		return status, nil
	}
	status.Installed = true

	// A job that is not loaded makes launchctl exit non-zero, which is
	// information rather than failure.
	out, err := commandOutput(ctx, "launchctl", "list", launchLabel)
	switch {
	case err != nil:
		status.State = StateStopped
	case strings.Contains(out, "\"PID\""):
		status.State = StateRunning
		status.Enabled = true
	default:
		status.State = StateStopped
		status.Enabled = true
	}
	return status, nil
}

// plist renders the launchd job definition. Every value is XML-escaped so a
// directory override containing markup cannot corrupt the file.
func (m *launchdManager) plist(opts Options) string {
	builder := &strings.Builder{}
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	builder.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	builder.WriteString(`<plist version="1.0">` + "\n")
	builder.WriteString("<dict>\n")
	builder.WriteString("\t<key>Label</key>\n\t<string>" + escapeXML(launchLabel) + "</string>\n")
	builder.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, arg := range ExecArgs(opts) {
		builder.WriteString("\t\t<string>" + escapeXML(arg) + "</string>\n")
	}
	builder.WriteString("\t</array>\n")
	builder.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	builder.WriteString("\t<key>KeepAlive</key>\n\t<true/>\n")
	builder.WriteString("\t<key>ThrottleInterval</key>\n\t<integer>10</integer>\n")
	builder.WriteString("</dict>\n")
	builder.WriteString("</plist>\n")
	return builder.String()
}

// escapeXML makes a value safe to embed in the generated plist.
func escapeXML(value string) string {
	escaped := &strings.Builder{}
	if err := xml.EscapeText(escaped, []byte(value)); err != nil {
		return ""
	}
	return escaped.String()
}
