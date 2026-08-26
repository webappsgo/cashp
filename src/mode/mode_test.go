package mode

import "testing"

func TestResolve(t *testing.T) {
	cases := []struct {
		name     string
		flag     string
		env      string
		expected Mode
	}{
		{"flag wins", "development", "debug", Development},
		{"env wins over default", "", "debug", Debug},
		{"default when nothing set", "", "", Production},
		{"flag alias prod", "prod", "", Production},
		{"flag alias dev", "dev", "", Development},
		{"unrecognized flag falls through to env", "bogus", "development", Development},
		{"unrecognized everything falls to default", "bogus", "bogus", Production},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("MODE", c.env)
			got := Resolve(c.flag)
			if got != c.expected {
				t.Errorf("Resolve(%q) with MODE=%q = %q, want %q", c.flag, c.env, got, c.expected)
			}
		})
	}
}

func TestResolveDebug(t *testing.T) {
	trueVal := true
	falseVal := false

	cases := []struct {
		name     string
		flag     *bool
		env      string
		mode     Mode
		expected bool
	}{
		{"flag true wins", &trueVal, "false", Production, true},
		{"flag false wins", &falseVal, "true", Debug, false},
		{"env true when no flag", nil, "true", Production, true},
		{"env 1 counts as true", nil, "1", Production, true},
		{"env false when no flag", nil, "false", Debug, false},
		{"mode-implied when no flag or env", nil, "", Debug, true},
		{"default false", nil, "", Production, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DEBUG", c.env)
			got := ResolveDebug(c.flag, c.mode)
			if got != c.expected {
				t.Errorf("ResolveDebug(%v, %q) with DEBUG=%q = %v, want %v", c.flag, c.mode, c.env, got, c.expected)
			}
		})
	}
}
