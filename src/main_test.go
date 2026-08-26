package main

import (
	"os"
	"testing"
)

func TestResolveColor(t *testing.T) {
	tests := []struct {
		name     string
		flag     string
		noColor  bool
		envColor string
		want     bool
	}{
		{"flag yes", "yes", false, "", true},
		{"flag no", "no", false, "", false},
		{"flag auto no-tty", "auto", false, "", false},
		{"flag auto with-tty", "auto", false, "", false},
		{"flag empty", "", false, "", false},
		{"no_color set", "", true, "", false},
		{"env COLOR yes", "", false, "yes", true},
		{"env COLOR no", "", false, "no", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldNoColor := os.Getenv("NO_COLOR")
			oldColor := os.Getenv("COLOR")
			defer func() {
				if oldNoColor != "" {
					os.Setenv("NO_COLOR", oldNoColor)
				} else {
					os.Unsetenv("NO_COLOR")
				}
				if oldColor != "" {
					os.Setenv("COLOR", oldColor)
				} else {
					os.Unsetenv("COLOR")
				}
			}()

			if tt.noColor {
				os.Setenv("NO_COLOR", "1")
			} else {
				os.Unsetenv("NO_COLOR")
			}

			if tt.envColor != "" {
				os.Setenv("COLOR", tt.envColor)
			} else {
				os.Unsetenv("COLOR")
			}

			got := resolveColor(tt.flag)
			if got != tt.want {
				t.Errorf("resolveColor(%q) = %v, want %v", tt.flag, got, tt.want)
			}
		})
	}
}

func TestParseFlags(t *testing.T) {
	t.Run("no flags", func(t *testing.T) {
		m, d, c := parseFlags([]string{})
		if m != "" {
			t.Errorf("mode = %q, want empty", m)
		}
		if d != nil {
			t.Errorf("debugPtr = %v, want nil (not set)", d)
		}
		if c != "" {
			t.Errorf("color = %q, want empty", c)
		}
	})

	t.Run("mode flag only", func(t *testing.T) {
		m, d, c := parseFlags([]string{"--mode", "development"})
		if m != "development" {
			t.Errorf("mode = %q, want development", m)
		}
		if d != nil {
			t.Errorf("debugPtr = %v, want nil (not set)", d)
		}
		if c != "" {
			t.Errorf("color = %q, want empty", c)
		}
	})

	t.Run("debug flag explicitly true", func(t *testing.T) {
		_, d, _ := parseFlags([]string{"--debug"})
		if d == nil || !*d {
			t.Errorf("debugPtr = %v, want pointer to true", d)
		}
	})

	t.Run("debug flag explicitly false", func(t *testing.T) {
		_, d, _ := parseFlags([]string{"--debug=false"})
		if d == nil || *d {
			t.Errorf("debugPtr = %v, want pointer to false", d)
		}
	})

	t.Run("both flags", func(t *testing.T) {
		m, d, _ := parseFlags([]string{"--mode", "debug", "--debug"})
		if m != "debug" {
			t.Errorf("mode = %q, want debug", m)
		}
		if d == nil || !*d {
			t.Errorf("debugPtr = %v, want pointer to true", d)
		}
	})
}
