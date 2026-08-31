//go:build windows

package service

// Detect returns the Windows service control manager backend. Windows has
// no per-user service equivalent, so unprivileged callers receive the
// system manager and get an informative elevation error from each operation
// instead of a UAC prompt they may not be able to satisfy.
func Detect() (Manager, error) {
	if !hasBinary("sc.exe") {
		return nil, ErrUnsupportedPlatform
	}
	return newWindowsManager(), nil
}
