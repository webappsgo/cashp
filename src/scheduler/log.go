package scheduler

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// logFileName is the scheduler's own log file inside Options.LogDir.
const logFileName = "scheduler.log"

// taskLogger writes scheduler activity to a file only. AI.md PART 19 and
// the Runtime Console Silence rule forbid the scheduler from printing to
// stdout or stderr, so no console writer exists anywhere in this package.
type taskLogger struct {
	mu     sync.Mutex
	file   *os.File
	logger *log.Logger
}

// newTaskLogger opens the scheduler log file under dir. An empty dir, or a
// directory that cannot be created or written, degrades to a discarding
// logger — the scheduler keeps running and still never touches the console.
func newTaskLogger(dir string) *taskLogger {
	discard := &taskLogger{logger: log.New(io.Discard, "", 0)}
	if dir == "" {
		return discard
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return discard
	}
	path := filepath.Join(dir, logFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return discard
	}
	return &taskLogger{file: f, logger: log.New(f, "", log.LstdFlags|log.LUTC)}
}

// Printf writes one formatted line to the scheduler log file.
func (l *taskLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger.Printf(format, args...)
}

// Close releases the log file handle.
func (l *taskLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	l.logger = log.New(io.Discard, "", 0)
	if err != nil {
		return fmt.Errorf("scheduler: close log: %w", err)
	}
	return nil
}
