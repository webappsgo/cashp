package version

import (
	"strings"
	"testing"
)

// TestDefaultsBeforeSet checks the values used by a binary built without
// ldflags.
func TestDefaultsBeforeSet(t *testing.T) {
	if Number() == "" || Commit() == "" {
		t.Fatal("the version and commit must never be empty")
	}
}

// TestSetAndGet checks the ldflags injector.
func TestSetAndGet(t *testing.T) {
	original := Get()
	t.Cleanup(func() {
		Set(original.Version, original.CommitID, original.BuildEpoch, original.BuildDate)
	})

	Set("1.2.3", "abcdef1", "1700000000", "2026-08-23")

	info := Get()
	if info.Version != "1.2.3" || info.CommitID != "abcdef1" {
		t.Fatalf("Get() = %+v", info)
	}
	if Number() != "1.2.3" {
		t.Errorf("Number() = %q", Number())
	}
	if Commit() != "abcdef1" {
		t.Errorf("Commit() = %q", Commit())
	}
	if BuildTime().Unix() != 1700000000 {
		t.Errorf("BuildTime() = %s", BuildTime())
	}
}

// TestSetIgnoresEmptyValues checks that a partial injection never blanks an
// existing value.
func TestSetIgnoresEmptyValues(t *testing.T) {
	original := Get()
	t.Cleanup(func() {
		Set(original.Version, original.CommitID, original.BuildEpoch, original.BuildDate)
	})

	Set("2.0.0", "cafe123", "1700000000", "2026-08-23")
	Set("", "", "", "")

	if Number() != "2.0.0" || Commit() != "cafe123" {
		t.Fatalf("an empty injection overwrote the version: %+v", Get())
	}
}

// TestUserAgentUsesHardcodedProjectName checks the rule that the
// User-Agent always carries the project name, never the binary name on
// disk, so a renamed or symlinked binary still identifies itself correctly.
func TestUserAgentUsesHardcodedProjectName(t *testing.T) {
	original := Get()
	t.Cleanup(func() {
		Set(original.Version, original.CommitID, original.BuildEpoch, original.BuildDate)
	})

	Set("3.1.4", "deadbee", "1700000000", "2026-08-23")

	want := ProjectName + "/3.1.4"
	if got := UserAgent(); got != want {
		t.Fatalf("UserAgent() = %q, want %q", got, want)
	}
	if ProjectName != "cashp" {
		t.Fatalf("the project name is frozen as cashp, got %q", ProjectName)
	}
	if ProjectOrg != "webappsgo" {
		t.Fatalf("the project org is frozen as webappsgo, got %q", ProjectOrg)
	}

	if got := UserAgentFor("cli"); got != "cashp-cli/3.1.4" {
		t.Fatalf("UserAgentFor(cli) = %q", got)
	}
	if got := UserAgentFor(""); got != want {
		t.Fatalf("UserAgentFor(\"\") = %q, want %q", got, want)
	}
}

// TestStringUsesBinaryName checks that display text uses the real binary
// name, which is what the user typed.
func TestStringUsesBinaryName(t *testing.T) {
	text := String()

	if !strings.Contains(text, BinaryName()) {
		t.Fatalf("String() = %q, want it to contain the binary name %q", text, BinaryName())
	}
	if !strings.Contains(text, Number()) {
		t.Fatalf("String() = %q, want it to contain the version", text)
	}
	if BinaryName() == "" {
		t.Error("BinaryName() must never be empty")
	}
}
