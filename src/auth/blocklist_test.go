package auth

import "testing"

func TestIsBlockedNameEmptyIsBlocked(t *testing.T) {
	for _, name := range []string{"", "   ", "\t"} {
		if !IsBlockedName(name) {
			t.Errorf("IsBlockedName(%q) = false, want true (empty/whitespace must be blocked)", name)
		}
	}
}

func TestIsBlockedNameExactMatch(t *testing.T) {
	if !IsBlockedName("cashp") {
		t.Error("IsBlockedName(\"cashp\") = false, want true")
	}
	if !IsBlockedName("CashP") {
		t.Error("IsBlockedName must be case-insensitive, \"CashP\" should be blocked")
	}
}

func TestIsBlockedNameSubstringMatch(t *testing.T) {
	cases := []string{"myadminpanel", "root-user", "system-admin", "official-store", "verified-account"}
	for _, name := range cases {
		if !IsBlockedName(name) {
			t.Errorf("IsBlockedName(%q) = false, want true (contains a blocked substring)", name)
		}
	}
}

func TestIsBlockedNameAllows(t *testing.T) {
	cases := []string{"alice", "bob-the-builder", "my-cool-project"}
	for _, name := range cases {
		if IsBlockedName(name) {
			t.Errorf("IsBlockedName(%q) = true, want false", name)
		}
	}
}

func TestAddBlocklistEntriesExtendsBlocklist(t *testing.T) {
	const custom = "totally-unique-test-entry-xyz"
	if IsBlockedName(custom) {
		t.Fatalf("precondition failed: %q was already blocked", custom)
	}
	AddBlocklistEntries([]string{custom})
	if !IsBlockedName(custom) {
		t.Errorf("IsBlockedName(%q) = false after AddBlocklistEntries, want true", custom)
	}
}

func TestBlockedTermInReturnsMatchedTerm(t *testing.T) {
	if got := BlockedTermIn("myadminpanel"); got != "admin" {
		t.Errorf("BlockedTermIn(%q) = %q, want %q", "myadminpanel", got, "admin")
	}
	if got := BlockedTermIn("cashp"); got != "cashp" {
		t.Errorf("BlockedTermIn(%q) = %q, want %q", "cashp", got, "cashp")
	}
	if got := BlockedTermIn("alice"); got != "" {
		t.Errorf("BlockedTermIn(%q) = %q, want empty string", "alice", got)
	}
}
