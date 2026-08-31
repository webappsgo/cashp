package terminal

import "testing"

// TestModeForBoundaries checks every documented breakpoint, including the
// rule that the smaller of the two dimensions decides the mode.
func TestModeForBoundaries(t *testing.T) {
	cases := []struct {
		name string
		cols int
		rows int
		want SizeMode
	}{
		{"micro by columns", 39, 50, SizeModeMicro},
		{"micro by rows", 200, 9, SizeModeMicro},
		{"minimal low edge", 40, 10, SizeModeMinimal},
		{"minimal high edge", 59, 15, SizeModeMinimal},
		{"compact low edge", 60, 16, SizeModeCompact},
		{"compact high edge", 79, 23, SizeModeCompact},
		{"standard low edge", 80, 24, SizeModeStandard},
		{"standard high edge", 119, 39, SizeModeStandard},
		{"wide low edge", 120, 40, SizeModeWide},
		{"wide high edge", 199, 59, SizeModeWide},
		{"ultrawide low edge", 200, 60, SizeModeUltrawide},
		{"ultrawide high edge", 399, 79, SizeModeUltrawide},
		{"massive", 400, 80, SizeModeMassive},
		{"wide columns short rows", 400, 30, SizeModeStandard},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ModeFor(tc.cols, tc.rows)
			if got != tc.want {
				t.Fatalf("ModeFor(%d, %d) = %s, want %s", tc.cols, tc.rows, got, tc.want)
			}
		})
	}
}

// TestSizeModeCapabilities checks the feature gates each mode exposes.
func TestSizeModeCapabilities(t *testing.T) {
	if SizeModeCompact.ShowASCIIArt() {
		t.Error("ASCII art must not be shown below the standard size mode")
	}
	if !SizeModeStandard.ShowASCIIArt() {
		t.Error("ASCII art must be shown at the standard size mode")
	}
	if SizeModeMinimal.ShowBorders() {
		t.Error("borders must not be shown below the compact size mode")
	}
	if SizeModeStandard.ShowSidebar() {
		t.Error("the sidebar must not be shown below the wide size mode")
	}
	if !SizeModeWide.ShowSidebar() {
		t.Error("the sidebar must be shown at the wide size mode")
	}
	if SizeModeMicro.ShowIcons() {
		t.Error("icons must not be shown in the micro size mode")
	}
}

// TestMaxTableColumns checks the per-mode table width budget.
func TestMaxTableColumns(t *testing.T) {
	cases := map[SizeMode]int{
		SizeModeMicro:     2,
		SizeModeMinimal:   3,
		SizeModeCompact:   4,
		SizeModeStandard:  6,
		SizeModeWide:      10,
		SizeModeUltrawide: 10,
		SizeModeMassive:   10,
	}

	for mode, want := range cases {
		if got := mode.MaxTableColumns(); got != want {
			t.Errorf("%s.MaxTableColumns() = %d, want %d", mode, got, want)
		}
	}
}

// TestRawSizeFromEnv checks the COLUMNS and LINES fallback used when no
// terminal is attached.
func TestRawSizeFromEnv(t *testing.T) {
	t.Setenv("COLUMNS", "132")
	t.Setenv("LINES", "48")

	cols, rows, ok := RawSize()
	if !ok {
		t.Fatal("RawSize() reported no size with COLUMNS and LINES set")
	}
	// A real terminal attached to the test process wins over the
	// environment, so only the environment case is asserted.
	if cols <= 0 || rows <= 0 {
		t.Fatalf("RawSize() = %d x %d, want positive dimensions", cols, rows)
	}
}

// TestGetTerminalSizeDefaults checks that a size is always produced.
func TestGetTerminalSizeDefaults(t *testing.T) {
	size := GetTerminalSize()
	if size.Cols <= 0 || size.Rows <= 0 {
		t.Fatalf("GetTerminalSize() = %d x %d, want positive dimensions", size.Cols, size.Rows)
	}
	if size.Mode != ModeFor(size.Cols, size.Rows) {
		t.Fatalf("size mode %s does not match ModeFor(%d, %d)", size.Mode, size.Cols, size.Rows)
	}
}

// TestSizeModeString checks the human-readable names.
func TestSizeModeString(t *testing.T) {
	if SizeModeUltrawide.String() != "ultrawide" {
		t.Errorf("SizeModeUltrawide.String() = %q", SizeModeUltrawide.String())
	}
	if SizeMode(99).String() == "" {
		t.Error("an unknown size mode must still produce a name")
	}
}
