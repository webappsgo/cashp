//go:build windows

package terminal

import (
	"context"
	"time"
)

// resizePollInterval is how often the terminal is measured on Windows,
// which has no SIGWINCH equivalent.
const resizePollInterval = 500 * time.Millisecond

// resizeEvents delivers a value on every poll tick. Watch compares the
// measured size against the cached one, so a tick without a real change
// costs nothing beyond the measurement.
func resizeEvents(ctx context.Context) (<-chan struct{}, func()) {
	ticker := time.NewTicker(resizePollInterval)
	events := make(chan struct{}, 1)

	go func() {
		defer close(events)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				select {
				case events <- struct{}{}:
				default:
				}
			}
		}
	}()

	return events, ticker.Stop
}
