package theme

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// IsSystemDarkTheme reports whether the operating system is set to a dark
// appearance. Detection is best effort per platform; when nothing can be
// determined the answer is dark, which is the product default.
func IsSystemDarkTheme() bool {
	if dark, ok := terminalDarkFromEnv(os.Getenv("COLORFGBG")); ok {
		return dark
	}

	switch runtime.GOOS {
	case "darwin":
		return darwinDarkTheme()
	case "windows":
		return windowsDarkTheme()
	default:
		return linuxDarkTheme()
	}
}

// terminalDarkFromEnv reads the COLORFGBG variable exported by several
// terminals. Its value is "foreground;background" (sometimes with a middle
// field) using ANSI colour indices; a background of 0-6 or 8 is dark.
func terminalDarkFromEnv(value string) (dark, ok bool) {
	parts := strings.Split(strings.TrimSpace(value), ";")
	if len(parts) < 2 {
		return false, false
	}
	background, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return false, false
	}
	return background <= 6 || background == 8, true
}

// darwinDarkTheme asks macOS for the global appearance setting. The
// AppleInterfaceStyle key only exists while dark mode is active.
func darwinDarkTheme() bool {
	output, err := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
	if err != nil {
		return true
	}
	return strings.Contains(strings.ToLower(string(output)), "dark")
}

// windowsDarkTheme reads the AppsUseLightTheme registry value, where 0
// means dark and 1 means light.
func windowsDarkTheme() bool {
	output, err := exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`,
		"/v", "AppsUseLightTheme").Output()
	if err != nil {
		return true
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return true
	}
	value := strings.ToLower(fields[len(fields)-1])
	return value == "0x0" || value == "0"
}

// linuxDarkTheme asks GNOME for the colour scheme, falling back to the
// legacy gtk-theme key whose name ends in "-dark" for dark variants.
func linuxDarkTheme() bool {
	if output, err := exec.Command("gsettings", "get", "org.gnome.desktop.interface", "color-scheme").Output(); err == nil {
		scheme := strings.ToLower(string(output))
		if strings.Contains(scheme, "dark") {
			return true
		}
		if strings.Contains(scheme, "light") {
			return false
		}
	}
	if output, err := exec.Command("gsettings", "get", "org.gnome.desktop.interface", "gtk-theme").Output(); err == nil {
		return strings.Contains(strings.ToLower(string(output)), "dark")
	}
	return true
}
