//go:build !linux && !darwin && !freebsd && !windows

package service

import (
	"fmt"
	"runtime"
)

// Detect reports that this build target has no service-manager
// implementation. The server itself still runs in the foreground.
func Detect() (Manager, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnsupportedPlatform, runtime.GOOS)
}
