//go:build !linux && !darwin && !windows && !freebsd

package service

// Detect reports that this platform has no service manager the agent knows
// how to drive. The agent still runs in the foreground, so an operator can
// supervise it with whatever their platform provides.
func Detect() (Manager, error) {
	return nil, ErrUnsupportedPlatform
}
