package config

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestBoolUnmarshalAcceptsWordForms(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		want bool
	}{
		{"value: yes", true},
		{"value: on", true},
		{"value: enabled", true},
		{"value: \"no\"", false},
		{"value: off", false},
		{"value: disabled", false},
	} {
		holder := struct {
			Value Bool `yaml:"value"`
		}{Value: NewBool(false)}

		if err := yaml.Unmarshal([]byte(tc.doc), &holder); err != nil {
			t.Fatalf("Unmarshal(%q) error = %v", tc.doc, err)
		}
		if holder.Value.Invalid {
			t.Errorf("Unmarshal(%q) flagged a recognized boolean as invalid", tc.doc)
		}
		if holder.Value.Value != tc.want {
			t.Errorf("Unmarshal(%q) = %t, want %t", tc.doc, holder.Value.Value, tc.want)
		}
	}
}

func TestBoolUnmarshalKeepsDefaultOnGarbage(t *testing.T) {
	holder := struct {
		Value Bool `yaml:"value"`
	}{Value: NewBool(true)}

	if err := yaml.Unmarshal([]byte("value: whenever"), &holder); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !holder.Value.Value {
		t.Error("an invalid boolean must keep its default value")
	}
	if !holder.Value.Invalid {
		t.Error("an invalid boolean must be flagged")
	}
	if holder.Value.Raw != "whenever" {
		t.Errorf("Raw = %q, want whenever", holder.Value.Raw)
	}
}

func TestBoolMarshalsCanonically(t *testing.T) {
	data, err := yaml.Marshal(map[string]Bool{"value": NewBool(true)})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != "value: true\n" {
		t.Errorf("Marshal() = %q, want %q", data, "value: true\n")
	}
}

func TestParseDuration(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"30", 30 * time.Second},
		{"45s", 45 * time.Second},
		{"5m", 5 * time.Minute},
		{"2h", 2 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
		{"1y", 365 * 24 * time.Hour},
		{"250ms", 250 * time.Millisecond},
		{"1h30m", 90 * time.Minute},
	} {
		got, err := ParseDuration(tc.in)
		if err != nil {
			t.Errorf("ParseDuration(%q) error = %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseDuration(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}

	for _, bad := range []string{"", "soon", "5 fortnights"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("ParseDuration(%q) should fail", bad)
		}
	}
}

func TestFormatDurationRoundTrips(t *testing.T) {
	// Each value is the canonical form of its own duration: 7d would come
	// back as 1w, so only self-canonical inputs belong in this list.
	for _, in := range []string{"1y", "1w", "30d", "5m", "45s", "0s"} {
		parsed, err := ParseDuration(in)
		if err != nil {
			t.Fatalf("ParseDuration(%q) error = %v", in, err)
		}
		if got := FormatDuration(parsed); got != in {
			t.Errorf("FormatDuration(ParseDuration(%q)) = %q", in, got)
		}
	}

	if got := FormatDuration(90 * time.Minute); got != "1h30m0s" {
		t.Errorf("FormatDuration(90m) = %q, want 1h30m0s", got)
	}
}

func TestParseSize(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
	}{
		{"1024", 1024},
		{"10MB", 10 << 20},
		{"10mb", 10 << 20},
		{"512k", 512 << 10},
		{"2GB", 2 << 30},
		{"1TB", 1 << 40},
	} {
		got, err := ParseSize(tc.in)
		if err != nil {
			t.Errorf("ParseSize(%q) error = %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseSize(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}

	for _, bad := range []string{"", "big", "-5", "10 apples"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) should fail", bad)
		}
	}
}

func TestFormatSize(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{10 << 20, "10MB"},
		{1 << 30, "1GB"},
		{1500, "1500"},
	} {
		if got := FormatSize(tc.in); got != tc.want {
			t.Errorf("FormatSize(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParsePortSpec(t *testing.T) {
	single, err := ParsePortSpec("64580")
	if err != nil || single.HTTP != 64580 || single.HTTPS != 0 {
		t.Errorf("ParsePortSpec(64580) = %+v, %v", single, err)
	}

	dual, err := ParsePortSpec("8090,8443")
	if err != nil || dual.HTTP != 8090 || dual.HTTPS != 8443 {
		t.Errorf("ParsePortSpec(8090,8443) = %+v, %v", dual, err)
	}
	if dual.String() != "8090,8443" {
		t.Errorf("String() = %q, want 8090,8443", dual.String())
	}

	for _, bad := range []string{"", "http", "1,2,3", "70000"} {
		if _, err := ParsePortSpec(bad); err == nil {
			t.Errorf("ParsePortSpec(%q) should fail", bad)
		}
	}
}

func TestPortSpecMarshal(t *testing.T) {
	data, err := yaml.Marshal(map[string]PortSpec{"port": NewPortSpec(64580)})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != "port: 64580\n" {
		t.Errorf("Marshal(single) = %q", data)
	}

	dual, err := ParsePortSpec("80,443")
	if err != nil {
		t.Fatalf("ParsePortSpec() error = %v", err)
	}
	data, err = yaml.Marshal(map[string]PortSpec{"port": dual})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != "port: 80,443\n" {
		t.Errorf("Marshal(dual) = %q, want %q", data, "port: 80,443\n")
	}
}

func TestSizeAndDurationUnmarshalKeepDefaults(t *testing.T) {
	holder := struct {
		Size     Size     `yaml:"size"`
		Duration Duration `yaml:"duration"`
	}{Size: NewSize(1024), Duration: NewDuration(time.Minute)}

	if err := yaml.Unmarshal([]byte("size: enormous\nduration: eventually\n"), &holder); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if holder.Size.Value != 1024 || !holder.Size.Invalid {
		t.Errorf("Size = %+v, want the 1024 default flagged invalid", holder.Size)
	}
	if holder.Duration.Value != time.Minute || !holder.Duration.Invalid {
		t.Errorf("Duration = %+v, want the 1m default flagged invalid", holder.Duration)
	}
}
