//go:build windows

package display

import (
	"os"
	"strings"
)

// detectPlatformDisplay performs Windows display detection.
//
// Windows always has an interactive desktop except when the process runs as
// a service in session 0. Services have no SESSIONNAME and no console, so
// the pair of checks below separates them from console and RDP sessions
// without leaving pure Go.
func (e *DisplayEnv) detectPlatformDisplay() {
	sessionName := os.Getenv("SESSIONNAME")
	hasConsole := IsTerminalFile(os.Stdout) || IsTerminalFile(os.Stdin)

	// No session and no console means session 0 — a Windows service.
	if sessionName == "" && !hasConsole {
		e.HasDisplay = false
		e.DisplayType = "none"
		return
	}

	// Remote desktop sessions are named RDP-Tcp#0, RDP-Tcp#1, and so on.
	if strings.HasPrefix(strings.ToUpper(sessionName), "RDP-") {
		e.HasDisplay = true
		e.DisplayType = "windows-rdp"
		return
	}

	e.HasDisplay = true
	e.DisplayType = "windows"
}
