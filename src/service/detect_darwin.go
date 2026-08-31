//go:build darwin

package service

// Detect returns the launchd manager. Root callers get the system daemon in
// /Library/LaunchDaemons; unprivileged callers get the per-user agent
// fallback (AI.md PART 24 "Service Installation Logic").
func Detect() (Manager, error) {
	if !hasBinary("launchctl") {
		return nil, ErrUnsupportedPlatform
	}
	return newLaunchdManager(!IsElevated()), nil
}
