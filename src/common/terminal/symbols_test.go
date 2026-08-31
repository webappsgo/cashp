package terminal

import (
	"testing"
	"unicode/utf8"
)

// TestSymbolsSelection checks that the ASCII set is returned whenever
// Unicode is unavailable, and that the ASCII set is pure ASCII.
func TestSymbolsSelection(t *testing.T) {
	if Symbols(true).Success != SymbolsUnicode.Success {
		t.Error("Symbols(true) must return the Unicode set")
	}
	if Symbols(false).Success != SymbolsASCII.Success {
		t.Error("Symbols(false) must return the ASCII set")
	}

	values := []string{
		SymbolsASCII.Success, SymbolsASCII.Error, SymbolsASCII.Warning,
		SymbolsASCII.Info, SymbolsASCII.Arrow, SymbolsASCII.Check,
		SymbolsASCII.Cross, SymbolsASCII.Bullet,
	}
	values = append(values, SymbolsASCII.Spinner...)

	for _, value := range values {
		if value == "" {
			t.Fatal("the ASCII symbol set must not contain empty values")
		}
		if utf8.RuneCountInString(value) != len([]byte(value)) {
			t.Fatalf("ASCII symbol %q contains a multi-byte rune", value)
		}
	}
}

// TestBoxSelection checks the box-drawing fallback.
func TestBoxSelection(t *testing.T) {
	if Box(true) != BoxUnicode {
		t.Error("Box(true) must return the Unicode box set")
	}
	if Box(false) != BoxASCII {
		t.Error("Box(false) must return the ASCII box set")
	}
	if BoxASCII == BoxUnicode {
		t.Error("the ASCII and Unicode box sets must differ")
	}
}

// TestSpinnerFramesPresent checks both spinners animate.
func TestSpinnerFramesPresent(t *testing.T) {
	if len(SymbolsUnicode.Spinner) < 2 {
		t.Error("the Unicode spinner needs at least two frames")
	}
	if len(SymbolsASCII.Spinner) < 2 {
		t.Error("the ASCII spinner needs at least two frames")
	}
}
