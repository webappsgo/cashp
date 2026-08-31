package nodes

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateNodeID(t *testing.T) {
	good := []string{
		"n1",
		"node-a",
		"node.a",
		"web-01.rack-3",
		strings.Repeat("a", MaxNodeIDLen),
	}
	for _, id := range good {
		if err := ValidateNodeID(id); err != nil {
			t.Fatalf("ValidateNodeID(%q) = %v, want nil", id, err)
		}
	}

	bad := []string{
		"",
		"a",
		strings.Repeat("a", MaxNodeIDLen+1),
		"-leading",
		"trailing-",
		".leading",
		"trailing.",
		"double--hyphen",
		"double..dot",
		"dot-.mix",
		"UPPER",
		"has space",
		"under_score",
		"semi;colon",
		"quote'drop",
		"../../etc/passwd",
		"node\x00a",
		"node$(id)",
		"node`id`",
		"node|nc",
	}
	for _, id := range bad {
		if err := ValidateNodeID(id); !errors.Is(err, ErrInvalidNodeID) {
			t.Fatalf("ValidateNodeID(%q) = %v, want ErrInvalidNodeID", id, err)
		}
	}
}

func TestValidateNodeName(t *testing.T) {
	good := []string{"Web 01", "node_a", "node-a.rack3", "A", strings.Repeat("n", MaxNodeNameLen)}
	for _, name := range good {
		if err := ValidateNodeName(name); err != nil {
			t.Fatalf("ValidateNodeName(%q) = %v, want nil", name, err)
		}
	}

	bad := []string{
		"",
		" leading",
		"trailing ",
		strings.Repeat("n", MaxNodeNameLen+1),
		"drop; table",
		"<script>",
		"name\nwith newline",
		"name\ttab",
		"emoji ☃",
	}
	for _, name := range bad {
		if err := ValidateNodeName(name); !errors.Is(err, ErrInvalidNodeName) {
			t.Fatalf("ValidateNodeName(%q) = %v, want ErrInvalidNodeName", name, err)
		}
	}
}

func TestValidateActionNameAllowlist(t *testing.T) {
	good := []string{"agent.ping", "hosting.deploy_site", "db1.backup"}
	for _, action := range good {
		if err := ValidateActionName(action); err != nil {
			t.Fatalf("ValidateActionName(%q) = %v, want nil", action, err)
		}
	}

	bad := []string{
		"",
		"ping",
		"a.b.c",
		".ping",
		"agent.",
		"Agent.Ping",
		"agent ping",
		"agent-ping.x",
		"agent.ping;rm -rf /",
		strings.Repeat("a", MaxActionLen+1),
	}
	for _, action := range bad {
		if err := ValidateActionName(action); !errors.Is(err, ErrUnknownAction) {
			t.Fatalf("ValidateActionName(%q) = %v, want ErrUnknownAction", action, err)
		}
	}
}

func TestValidateFactsNormalizes(t *testing.T) {
	in := Facts{
		OS:          "  LINUX ",
		Arch:        " AMD64",
		Kernel:      "6.6.0-cashp",
		Hostname:    "NODE-A.EXAMPLE",
		CPUCores:    8,
		MemoryBytes: 16 << 30,
		DiskBytes:   0,
		Backends:    []string{"PODMAN", " docker ", "docker", "systemd"},
	}
	out, err := ValidateFacts(in)
	if err != nil {
		t.Fatalf("ValidateFacts: %v", err)
	}
	if out.OS != "linux" || out.Arch != "amd64" || out.Hostname != "node-a.example" {
		t.Fatalf("facts = %+v", out)
	}
	if len(out.Backends) != 3 {
		t.Fatalf("backends = %v, want the duplicate collapsed", out.Backends)
	}
	for i := 1; i < len(out.Backends); i++ {
		if out.Backends[i-1] >= out.Backends[i] {
			t.Fatalf("backends are not sorted: %v", out.Backends)
		}
	}
	if out.Backends[0] != "docker" || out.Backends[1] != "podman" || out.Backends[2] != "systemd" {
		t.Fatalf("backends = %v", out.Backends)
	}
}

func TestValidateFactsRejectsHostileInput(t *testing.T) {
	base := goodFacts()
	mutate := func(fn func(f *Facts)) Facts {
		f := goodFacts()
		fn(&f)
		return f
	}

	cases := []struct {
		name  string
		facts Facts
	}{
		{"empty os", mutate(func(f *Facts) { f.OS = "" })},
		{"unsupported os", mutate(func(f *Facts) { f.OS = "windows" })},
		{"injected os", mutate(func(f *Facts) { f.OS = "linux; rm -rf /" })},
		{"unknown arch", mutate(func(f *Facts) { f.Arch = "vax" })},
		{"empty kernel", mutate(func(f *Facts) { f.Kernel = "" })},
		{"kernel with a newline", mutate(func(f *Facts) { f.Kernel = "6.6.0\ninjected" })},
		{"kernel with a null byte", mutate(func(f *Facts) { f.Kernel = "6.6.0\x00" })},
		{"path traversal hostname", mutate(func(f *Facts) { f.Hostname = "../../etc/passwd" })},
		{"hostname with a space", mutate(func(f *Facts) { f.Hostname = "node a" })},
		{"zero cores", mutate(func(f *Facts) { f.CPUCores = 0 })},
		{"negative cores", mutate(func(f *Facts) { f.CPUCores = -1 })},
		{"absurd cores", mutate(func(f *Facts) { f.CPUCores = MaxCPUCores + 1 })},
		{"zero memory", mutate(func(f *Facts) { f.MemoryBytes = 0 })},
		{"absurd memory", mutate(func(f *Facts) { f.MemoryBytes = MaxMemoryBytes + 1 })},
		{"negative disk", mutate(func(f *Facts) { f.DiskBytes = -1 })},
		{"absurd disk", mutate(func(f *Facts) { f.DiskBytes = MaxDiskBytes + 1 })},
		{"unknown backend", mutate(func(f *Facts) { f.Backends = []string{"docker", "rootkit"} })},
		{"injected backend", mutate(func(f *Facts) { f.Backends = []string{"docker; curl evil"} })},
		{"too many backends", mutate(func(f *Facts) { f.Backends = make([]string, MaxBackends+1) })},
	}
	for _, tc := range cases {
		if _, err := ValidateFacts(tc.facts); !errors.Is(err, ErrInvalidFacts) {
			t.Fatalf("%s: ValidateFacts error = %v, want ErrInvalidFacts", tc.name, err)
		}
	}

	// An over-long kernel string is truncated rather than rejected, so a
	// chatty node cannot store an unbounded value either.
	long := base
	long.Kernel = strings.Repeat("k", MaxKernelLen*4)
	out, err := ValidateFacts(long)
	if err != nil {
		t.Fatalf("ValidateFacts: %v", err)
	}
	if len(out.Kernel) != MaxKernelLen {
		t.Fatalf("kernel kept %d bytes, want %d", len(out.Kernel), MaxKernelLen)
	}
}

func TestIsPrintableToken(t *testing.T) {
	good := []string{"6.6.0-cashp", "v1.2.3", "~"}
	for _, s := range good {
		if !isPrintableToken(s) {
			t.Fatalf("isPrintableToken(%q) = false", s)
		}
	}
	bad := []string{"has space", "tab\there", "null\x00", "del\x7f", "utfé"}
	for _, s := range bad {
		if isPrintableToken(s) {
			t.Fatalf("isPrintableToken(%q) = true", s)
		}
	}
}

func TestSortStrings(t *testing.T) {
	in := []string{"podman", "apk", "docker", "apk"}
	sortStrings(in)
	want := []string{"apk", "apk", "docker", "podman"}
	for i := range want {
		if in[i] != want[i] {
			t.Fatalf("sorted = %v, want %v", in, want)
		}
	}
	sortStrings(nil)
}
