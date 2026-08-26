package config

import "testing"

func TestParseBool(t *testing.T) {
	cases := []struct {
		input      string
		defaultVal bool
		want       bool
		wantErr    bool
	}{
		{"", true, true, false},
		{"", false, false, false},
		{"true", false, true, false},
		{"YES", false, true, false},
		{"  on  ", false, true, false},
		{"false", true, false, false},
		{"NO", true, false, false},
		{"off", true, false, false},
		{"nope", true, false, false},
		{"totally", false, true, false},
		{"garbage", false, false, true},
	}

	for _, c := range cases {
		got, err := ParseBool(c.input, c.defaultVal)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseBool(%q, %v) error = %v, wantErr %v", c.input, c.defaultVal, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("ParseBool(%q, %v) = %v, want %v", c.input, c.defaultVal, got, c.want)
		}
	}
}

func TestIsTruthy(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"true", true},
		{"yes", true},
		{"enabled", true},
		{"false", false},
		{"no", false},
		{"", false},
		{"garbage", false},
	}

	for _, c := range cases {
		if got := IsTruthy(c.input); got != c.want {
			t.Errorf("IsTruthy(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}
