package terminal

import (
	"context"
	"sync"
)

var (
	currentMu   sync.RWMutex
	currentSize = TerminalSize{Cols: DefaultCols, Rows: DefaultRows, Mode: ModeFor(DefaultCols, DefaultRows)}
	currentSet  bool
)

// Current returns the most recently observed terminal size. Before Watch
// has reported anything it queries the terminal directly, so it is always
// safe to call.
func Current() TerminalSize {
	currentMu.RLock()
	size, set := currentSize, currentSet
	currentMu.RUnlock()
	if set {
		return size
	}
	return GetTerminalSize()
}

// setCurrent stores a newly observed size and reports whether it changed.
func setCurrent(size TerminalSize) bool {
	currentMu.Lock()
	defer currentMu.Unlock()
	changed := !currentSet || currentSize.Cols != size.Cols || currentSize.Rows != size.Rows
	currentSize = size
	currentSet = true
	return changed
}

// Watch keeps the cached terminal size up to date and invokes onResize for
// every change until ctx is cancelled. On Unix it is driven by SIGWINCH; on
// Windows, which has no such signal, by polling. It blocks, so callers
// normally run it in its own goroutine.
func Watch(ctx context.Context, onResize func(TerminalSize)) {
	size := GetTerminalSize()
	if setCurrent(size) && onResize != nil {
		onResize(size)
	}

	events, stop := resizeEvents(ctx)
	defer stop()

	for {
		select {
		case <-ctx.Done():
			return
		case _, open := <-events:
			if !open {
				return
			}
			size := GetTerminalSize()
			if setCurrent(size) && onResize != nil {
				onResize(size)
			}
		}
	}
}
