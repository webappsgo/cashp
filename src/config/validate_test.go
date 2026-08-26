package config

import "testing"

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"defaults ok", *Defaults(), false},
		{"empty mode ok", Config{Mode: "", Database: DatabaseConfig{Driver: ""}}, false},
		{"valid mode/driver", Config{Mode: "development", Database: DatabaseConfig{Driver: "postgres"}}, false},
		{"invalid mode", Config{Mode: "bogus"}, true},
		{"port too high", Config{Port: 70000}, true},
		{"port negative", Config{Port: -1}, true},
		{"port zero is unset, ok", Config{Port: 0}, false},
		{"port valid", Config{Port: 8080}, false},
		{"invalid driver", Config{Database: DatabaseConfig{Driver: "bogus"}}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(&c.cfg)
			if (err != nil) != c.wantErr {
				t.Errorf("Validate(%+v) error = %v, wantErr %v", c.cfg, err, c.wantErr)
			}
		})
	}
}

func TestParsePort(t *testing.T) {
	cases := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"8080", 8080, false},
		{"0", 0, false},
		{"65535", 65535, false},
		{"70000", 0, true},
		{"-1", 0, true},
		{"notanumber", 0, true},
	}

	for _, c := range cases {
		got, err := parsePort(c.input)
		if (err != nil) != c.wantErr {
			t.Errorf("parsePort(%q) error = %v, wantErr %v", c.input, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("parsePort(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}
