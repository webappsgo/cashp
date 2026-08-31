package display

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/common/terminal"
)

// spinnerInterval is how often an animated spinner advances a frame.
const spinnerInterval = 100 * time.Millisecond

// progressBarWidth is the character width of the ANSI progress bar.
const progressBarWidth = 50

// Spinner shows that work is in progress. Implementations degrade from an
// animated ANSI spinner to plain text on dumb terminals.
type Spinner interface {
	// Start begins showing progress.
	Start()
	// Update replaces the message shown next to the spinner.
	Update(message string)
	// Stop ends the spinner and prints the final message.
	Stop()
}

// NewSpinner returns a spinner appropriate for the display environment.
func NewSpinner(env *DisplayEnv, message string) Spinner {
	if env == nil || !CanUseANSI(env) {
		return &TextSpinner{out: os.Stdout, message: message}
	}
	return &ANSISpinner{
		out:     os.Stdout,
		message: message,
		frames:  terminal.Symbols(env.UseUnicodeSymbols()).Spinner,
		done:    make(chan struct{}),
	}
}

// TextSpinner is the dumb-terminal fallback: it prints "Processing..." once
// and "Done." when stopped, with no cursor control.
type TextSpinner struct {
	mu      sync.Mutex
	out     io.Writer
	message string
	started bool
}

// Start prints the initial processing line.
func (s *TextSpinner) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return
	}
	s.started = true
	fmt.Fprintf(s.out, "%s...\n", s.message)
}

// Update prints the new message on its own line.
func (s *TextSpinner) Update(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.message = message
	if s.started {
		fmt.Fprintf(s.out, "%s...\n", message)
	}
}

// Stop prints the completion line.
func (s *TextSpinner) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return
	}
	s.started = false
	fmt.Fprintln(s.out, "Done.")
}

// ANSISpinner animates a spinner using carriage returns.
type ANSISpinner struct {
	mu      sync.Mutex
	out     io.Writer
	message string
	frames  []string
	done    chan struct{}
	started bool
}

// Start launches the animation goroutine.
func (s *ANSISpinner) Start() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	if len(s.frames) == 0 {
		s.frames = terminal.SymbolsASCII.Spinner
	}
	s.mu.Unlock()

	go s.animate()
}

// animate advances the spinner until Stop closes the done channel.
func (s *ANSISpinner) animate() {
	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()

	frame := 0
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.mu.Lock()
			fmt.Fprintf(s.out, "\r%s %s", s.frames[frame%len(s.frames)], s.message)
			s.mu.Unlock()
			frame++
		}
	}
}

// Update swaps the message shown next to the spinner.
func (s *ANSISpinner) Update(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.message = message
}

// Stop halts the animation and clears the spinner line.
func (s *ANSISpinner) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.started = false
	message := s.message
	s.mu.Unlock()

	close(s.done)
	fmt.Fprintf(s.out, "\r%s\r%s\n", strings.Repeat(" ", len(message)+2), message)
}

// ShowProgress renders progress. Dumb terminals get a plain percentage
// line; everything else gets an ANSI bar redrawn in place.
func ShowProgress(env *DisplayEnv, percent int) {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	if env == nil || !CanUseANSI(env) {
		fmt.Printf("%d%% complete\n", percent)
		return
	}
	filled := percent * progressBarWidth / 100
	fmt.Printf("\r[%-*s] %d%%", progressBarWidth, strings.Repeat("=", filled), percent)
	if percent == 100 {
		fmt.Println()
	}
}
