//go:build !linux && !freebsd && !netbsd && !openbsd && !dragonfly && !darwin

package terminal

// termSize has no kernel query available on this platform, so RawSize
// falls through to the COLUMNS/LINES environment variables and finally to
// the 80x24 default.
func termSize(fd uintptr) (cols, rows int, ok bool) {
	return 0, 0, false
}
