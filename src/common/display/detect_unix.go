//go:build !windows

package display

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// detectPlatformDisplay performs Unix and macOS display detection.
func (e *DisplayEnv) detectPlatformDisplay() {
	// Wayland is preferred over X11 on Linux.
	if waylandDisplay := os.Getenv("WAYLAND_DISPLAY"); waylandDisplay != "" {
		e.HasDisplay = true
		e.DisplayType = "wayland"
		return
	}

	if display := os.Getenv("DISPLAY"); display != "" {
		e.HasDisplay = true
		e.DisplayType = "x11"
		return
	}

	if runtime.GOOS == "darwin" {
		// On macOS a display exists unless we are on SSH or running as a
		// LaunchDaemon with no GUI session.
		if !e.IsSSH && os.Getenv("__CFBundleIdentifier") != "" {
			e.HasDisplay = true
			e.DisplayType = "macos"
			return
		}
		// Fall back to asking launchd which session manager owns us.
		cmd := exec.Command("launchctl", "managername")
		if output, err := cmd.Output(); err == nil {
			if strings.Contains(string(output), "Aqua") {
				e.HasDisplay = true
				e.DisplayType = "macos"
				return
			}
		}
	}

	e.HasDisplay = false
	e.DisplayType = "none"
}
