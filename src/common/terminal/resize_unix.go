//go:build !windows

package terminal

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// resizeEvents delivers a value every time the terminal is resized. The
// kernel raises SIGWINCH on every geometry change, so no polling is needed.
// The returned stop function releases the signal registration.
func resizeEvents(ctx context.Context) (<-chan struct{}, func()) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)

	events := make(chan struct{}, 1)

	go func() {
		defer close(events)
		for {
			select {
			case <-ctx.Done():
				return
			case _, open := <-sigCh:
				if !open {
					return
				}
				select {
				case events <- struct{}{}:
				default:
				}
			}
		}
	}()

	return events, func() { signal.Stop(sigCh) }
}
