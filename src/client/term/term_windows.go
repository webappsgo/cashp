//go:build windows

package term

// osWidth has no dependency-free way to query the console buffer, so the
// COLUMNS environment variable and the default width are used instead.
func osWidth() int {
	return 0
}

// disableEcho is unavailable on Windows without a console API binding, so
// it reports false and the caller warns that input will be visible.
func disableEcho() (restore func(), disabled bool) {
	return func() {}, false
}
