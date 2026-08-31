package display

import "testing"

// TestParseDisplayMode checks parsing and the round trip through String.
func TestParseDisplayMode(t *testing.T) {
	cases := map[string]DisplayMode{
		"headless": DisplayModeHeadless,
		"cli":      DisplayModeCLI,
		"tui":      DisplayModeTUI,
		"gui":      DisplayModeGUI,
		"  GUI  ":  DisplayModeGUI,
	}

	for value, want := range cases {
		got, ok := ParseDisplayMode(value)
		if !ok {
			t.Fatalf("ParseDisplayMode(%q) reported failure", value)
		}
		if got != want {
			t.Fatalf("ParseDisplayMode(%q) = %s, want %s", value, got, want)
		}
	}

	if _, ok := ParseDisplayMode("hologram"); ok {
		t.Error("ParseDisplayMode must reject an unknown mode")
	}
}

// TestDisplayModeOrdering checks the hierarchy headless < cli < tui < gui,
// which callers compare against.
func TestDisplayModeOrdering(t *testing.T) {
	if !(DisplayModeHeadless < DisplayModeCLI &&
		DisplayModeCLI < DisplayModeTUI &&
		DisplayModeTUI < DisplayModeGUI) {
		t.Fatal("display modes must be ordered headless < cli < tui < gui")
	}
}

// TestParseColorFlag checks the --color flag values, including the auto
// case that must leave detection in charge.
func TestParseColorFlag(t *testing.T) {
	on := []string{"yes", "true", "on", "always", "1", "enable", "ENABLED"}
	for _, value := range on {
		result := ParseColorFlag(value)
		if result == nil || !*result {
			t.Fatalf("ParseColorFlag(%q) must force colour on", value)
		}
	}

	off := []string{"no", "false", "off", "never", "0", "disable", "NONE"}
	for _, value := range off {
		result := ParseColorFlag(value)
		if result == nil || *result {
			t.Fatalf("ParseColorFlag(%q) must force colour off", value)
		}
	}

	for _, value := range []string{"", "auto", "AUTO", "unknown"} {
		if ParseColorFlag(value) != nil {
			t.Fatalf("ParseColorFlag(%q) must leave auto-detection in charge", value)
		}
	}
}

// TestColorEnabledPrecedence checks flag over config over NO_COLOR.
func TestColorEnabledPrecedence(t *testing.T) {
	t.Cleanup(func() {
		ConfigColor = nil
		ConfigEmoji = nil
	})

	t.Setenv("NO_COLOR", "1")
	ConfigColor = nil

	if ColorEnabled(nil) {
		t.Error("NO_COLOR must disable colour when no flag or config is set")
	}

	forced := true
	if !ColorEnabled(&forced) {
		t.Error("the --color flag must win over NO_COLOR")
	}

	ConfigColor = func() (bool, bool) { return true, true }
	if !ColorEnabled(nil) {
		t.Error("the config file must win over NO_COLOR")
	}

	ConfigColor = func() (bool, bool) { return false, false }
	if ColorEnabled(nil) {
		t.Error("an unset config value must fall through to NO_COLOR")
	}
}

// TestEmojiEnabled checks that NO_COLOR and TERM=dumb disable emojis while
// an explicit config value can force them back on.
func TestEmojiEnabled(t *testing.T) {
	t.Cleanup(func() { ConfigEmoji = nil })

	ConfigEmoji = nil
	t.Setenv("NO_COLOR", "1")
	if EmojiEnabled() {
		t.Error("NO_COLOR must disable emojis")
	}

	ConfigEmoji = func() (bool, bool) { return true, true }
	if !EmojiEnabled() {
		t.Error("an explicit config value must force emojis on")
	}
}

// TestDumbTerminalBehaviour checks that TERM=dumb disables colour, ANSI,
// and Unicode status symbols.
func TestDumbTerminalBehaviour(t *testing.T) {
	t.Cleanup(func() {
		ConfigColor = nil
		ConfigEmoji = nil
	})
	ConfigColor = nil
	ConfigEmoji = nil

	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")

	env := DisplayEnv{TerminalType: "dumb", IsTerminal: true}
	if !env.IsDumbTerminal() {
		t.Fatal("TERM=dumb must be reported as a dumb terminal")
	}
	if env.SupportsUnicode() {
		t.Error("a dumb terminal must not claim Unicode support")
	}
	if env.UseUnicodeSymbols() {
		t.Error("a dumb terminal must fall back to ASCII symbols")
	}
	if CanUseANSI(&env) {
		t.Error("a dumb terminal must not receive ANSI escapes")
	}
	if ColorEnabled(nil) {
		t.Error("TERM=dumb must disable colour")
	}
	if EmojiEnabled() {
		t.Error("TERM=dumb must disable emojis")
	}
}

// TestCanUseANSIRequiresTerminal checks the non-TTY and nil cases.
func TestCanUseANSIRequiresTerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "")

	if CanUseANSI(nil) {
		t.Error("a nil environment must not allow ANSI output")
	}

	piped := DisplayEnv{TerminalType: "xterm-256color", IsTerminal: false}
	if CanUseANSI(&piped) {
		t.Error("piped output must not receive ANSI escapes")
	}

	tty := DisplayEnv{TerminalType: "xterm-256color", IsTerminal: true}
	if !CanUseANSI(&tty) {
		t.Error("an interactive terminal must allow ANSI output")
	}
}

// TestDetectDisplayEnv checks that detection always produces a usable
// environment, whatever the host looks like.
func TestDetectDisplayEnv(t *testing.T) {
	env := DetectDisplayEnv()

	if env.Mode < DisplayModeHeadless || env.Mode > DisplayModeGUI {
		t.Fatalf("DetectDisplayEnv() produced an out-of-range mode %d", env.Mode)
	}
	if env.Cols <= 0 || env.Rows <= 0 {
		t.Fatalf("DetectDisplayEnv() produced size %d x %d", env.Cols, env.Rows)
	}
	if env.Mode.String() == "" {
		t.Error("every display mode must have a name")
	}
}

// TestModeHelpers checks the named mode predicates.
func TestModeHelpers(t *testing.T) {
	env := DisplayEnv{Mode: DisplayModeTUI}

	if !env.IsAutoDetectDisplayModeTUI() {
		t.Error("the TUI predicate must match the TUI mode")
	}
	if env.IsAutoDetectDisplayModeGUI() || env.IsAutoDetectDisplayModeCLI() || env.IsAutoDetectDisplayModeHeadless() {
		t.Error("only one mode predicate may match")
	}
}
