//go:build freebsd

package service

// Detect returns the rc.d manager. FreeBSD has no per-user service
// equivalent, so unprivileged callers receive the system manager and get an
// informative elevation error from each operation instead of a prompt they
// could not satisfy.
func Detect() (Manager, error) {
	if !hasBinary("service") {
		return nil, ErrUnsupportedPlatform
	}
	return newRCDManager(), nil
}
