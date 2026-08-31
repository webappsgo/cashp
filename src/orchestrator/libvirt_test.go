package orchestrator

import (
	"context"
	"strings"
	"testing"
)

// vmSpec is the representative virtual machine the domain-XML tests build.
func vmSpec() Spec {
	return Spec{
		Ref:       Ref{Class: ClassTenant, TenantID: "acme", Name: "db"},
		Kind:      KindVM,
		Image:     "debian-12",
		Resources: Resources{CPUCores: 2, MemoryBytes: 2 * 1024 * 1024 * 1024},
		Disks: []Disk{{
			Source: "disks/root.qcow2",
			Format: "qcow2",
			Target: "vda",
			Bus:    "virtio",
			Boot:   true,
		}},
		Firmware: FirmwareUEFI,
	}
}

// resolvedVM resolves the representative VM the way the libvirt backend does,
// including the hypervisor network substitution.
func resolvedVM(t *testing.T) resolvedSpec {
	t.Helper()
	resolved, err := testConfig().resolveSpec(vmSpec())
	if err != nil {
		t.Fatalf("resolveSpec: %v", err)
	}
	resolved.NetworkName = DefaultLibvirtNetwork
	return resolved
}

// subcommand returns the virsh subcommand out of a recorded argv, which
// always starts with the connection flag.
func subcommand(args []string) string {
	if len(args) < 3 {
		return ""
	}
	return args[2]
}

// scriptedLibvirt builds a libvirt backend over a FakeRunner that answers
// dumpxml with a real generated definition, so lifecycle calls resolve
// ownership exactly as they would against a hypervisor.
func scriptedLibvirt(t *testing.T, extra func(args []string) (RunResult, bool)) (*LibvirtBackend, *FakeRunner) {
	t.Helper()

	definition, err := buildDomainXML(testConfig(), resolvedVM(t))
	if err != nil {
		t.Fatalf("buildDomainXML: %v", err)
	}

	runner := &FakeRunner{Respond: func(bin string, args []string, stdin []byte) (RunResult, error) {
		if extra != nil {
			if result, handled := extra(args); handled {
				return result, nil
			}
		}
		switch subcommand(args) {
		case "dumpxml":
			return RunResult{Stdout: definition}, nil
		case "domstate":
			return RunResult{Stdout: []byte("running\n")}, nil
		default:
			return RunResult{}, nil
		}
	}}

	backend, err := NewLibvirtBackend(testConfig(), runner)
	if err != nil {
		t.Fatalf("NewLibvirtBackend: %v", err)
	}
	return backend, runner
}

// TestBuildDomainXMLForRepresentativeVM pins the generated definition for a
// realistic guest.
func TestBuildDomainXMLForRepresentativeVM(t *testing.T) {
	encoded, err := buildDomainXML(testConfig(), resolvedVM(t))
	if err != nil {
		t.Fatalf("buildDomainXML: %v", err)
	}
	text := string(encoded)

	for _, want := range []string{
		`<domain type="kvm">`,
		`<name>cashp-t-acme--db</name>`,
		domainNamespace,
		`acme`,
		`<memory unit="KiB">2097152</memory>`,
		`<vcpu placement="static">2</vcpu>`,
		`<type arch="x86_64" machine="q35">hvm</type>`,
		`<driver name="qemu" type="qcow2">`,
		`<source file="/srv/cashp/tenants/acme/disks/root.qcow2">`,
		`<target dev="vda" bus="virtio">`,
		`<boot order="1">`,
		`<source network="default">`,
		domainDefaultLoader,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated domain XML is missing %q:\n%s", want, text)
		}
	}
	if !strings.HasSuffix(text, "\n") {
		t.Fatal("the definition must end with a single newline")
	}

	parsed, err := decodeDomain(encoded)
	if err != nil {
		t.Fatalf("decodeDomain: %v", err)
	}
	owner, ok := parseDomainOwner(parsed)
	if !ok {
		t.Fatal("the ownership block did not round-trip")
	}
	if owner != (Ref{Class: ClassTenant, TenantID: "acme", Name: "db"}) {
		t.Fatalf("unexpected owner %#v", owner)
	}
	if res := parseDomainResources(parsed); res.MemoryBytes != 2*1024*1024*1024 || res.CPUCores != 2 {
		t.Fatalf("unexpected resources %#v", res)
	}
}

// TestBuildDomainXMLWithoutNetworkOmitsInterface checks that a guest asked
// for no network gets no interface at all.
func TestBuildDomainXMLWithoutNetworkOmitsInterface(t *testing.T) {
	spec := vmSpec()
	spec.Network = Network{Mode: NetworkNone}
	resolved, err := testConfig().resolveSpec(spec)
	if err != nil {
		t.Fatalf("resolveSpec: %v", err)
	}
	encoded, err := buildDomainXML(testConfig(), resolved)
	if err != nil {
		t.Fatalf("buildDomainXML: %v", err)
	}
	if strings.Contains(string(encoded), "<interface") {
		t.Fatalf("an interface was emitted for a networkless guest:\n%s", encoded)
	}
}

// TestBuildDomainXMLRefusesIncompleteSpecs keeps a half-described guest from
// ever reaching the hypervisor.
func TestBuildDomainXMLRefusesIncompleteSpecs(t *testing.T) {
	container, err := testConfig().resolveSpec(tenantSpec())
	if err != nil {
		t.Fatalf("resolveSpec: %v", err)
	}
	if _, err := buildDomainXML(testConfig(), container); !IsUnsupported(err) {
		t.Fatalf("expected an unsupported-operation error for a container, got %v", err)
	}

	noDisk := resolvedVM(t)
	noDisk.Disks = nil
	if _, err := buildDomainXML(testConfig(), noDisk); !IsValidation(err) {
		t.Fatalf("expected a validation error for a diskless guest, got %v", err)
	}

	noMemory := resolvedVM(t)
	noMemory.Spec.Resources.MemoryBytes = 0
	if _, err := buildDomainXML(testConfig(), noMemory); !IsValidation(err) {
		t.Fatalf("expected a validation error for a memoryless guest, got %v", err)
	}
}

// TestValidateDomainXMLCatchesTampering proves the round-trip check is what
// stands between a mismatched document and the hypervisor.
func TestValidateDomainXMLCatchesTampering(t *testing.T) {
	resolved := resolvedVM(t)
	encoded, err := buildDomainXML(testConfig(), resolved)
	if err != nil {
		t.Fatalf("buildDomainXML: %v", err)
	}

	swapped := resolved
	swapped.Qualified = "cashp-t-other--db"
	if err := validateDomainXML(encoded, swapped); !IsValidation(err) {
		t.Fatalf("a renamed domain was accepted: %v", err)
	}

	marked := append(append([]byte(nil), encoded...), []byte("]]>")...)
	if err := validateDomainXML(marked, resolved); !IsValidation(err) {
		t.Fatalf("an unsafe marker was accepted: %v", err)
	}
}

// TestLibvirtCreateDefinesFromStdin checks the exact argv and that the
// definition is piped rather than written to a file on the host.
func TestLibvirtCreateDefinesFromStdin(t *testing.T) {
	backend, runner := scriptedLibvirt(t, nil)

	resolved, err := testConfig().resolveSpec(vmSpec())
	if err != nil {
		t.Fatalf("resolveSpec: %v", err)
	}
	instance, err := backend.Create(context.Background(), resolved)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if instance.QualifiedName != "cashp-t-acme--db" || instance.State != StateRunning {
		t.Fatalf("unexpected instance %#v", instance)
	}

	commands := runner.Commands()
	if len(commands) == 0 {
		t.Fatal("no command was run")
	}
	define := commands[0]
	if define.Bin != DefaultVirshBinary {
		t.Fatalf("unexpected binary %q", define.Bin)
	}
	want := []string{"--connect", DefaultLibvirtURI, "define", libvirtStdinFile}
	if strings.Join(define.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %#v, want %#v", define.Args, want)
	}
	if !strings.Contains(string(define.Stdin), "<name>cashp-t-acme--db</name>") {
		t.Fatalf("the domain definition was not piped on stdin: %q", define.Stdin)
	}
	for _, arg := range define.Args {
		if strings.ContainsAny(arg, ";|&$`<>") {
			t.Fatalf("argv entry %q carries shell syntax", arg)
		}
	}
}

// TestLibvirtRemoveUndefinesNVRAM checks the destroy-then-undefine sequence
// and that UEFI variable storage is cleaned up with the guest.
func TestLibvirtRemoveUndefinesNVRAM(t *testing.T) {
	backend, runner := scriptedLibvirt(t, nil)

	ref := Ref{Class: ClassTenant, TenantID: "acme", Name: "db"}
	if err := backend.Remove(context.Background(), ref, RemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	var sequence []string
	for _, cmd := range runner.Commands() {
		sequence = append(sequence, strings.Join(cmd.Args[2:], " "))
	}
	joined := strings.Join(sequence, "|")
	if !strings.Contains(joined, "destroy cashp-t-acme--db") {
		t.Fatalf("a forced removal must power the guest off first: %v", sequence)
	}
	if !strings.Contains(joined, "undefine cashp-t-acme--db --nvram --remove-all-storage") {
		t.Fatalf("unexpected undefine sequence: %v", sequence)
	}
}

// TestLibvirtStopChoosesShutdownOrDestroy checks that a grace period asks the
// guest to shut down cleanly and a zero grace powers it off.
func TestLibvirtStopChoosesShutdownOrDestroy(t *testing.T) {
	ref := Ref{Class: ClassTenant, TenantID: "acme", Name: "db"}

	graceful, gracefulRunner := scriptedLibvirt(t, nil)
	if err := graceful.Stop(context.Background(), ref, DefaultStopGrace); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !ranSubcommand(gracefulRunner, "shutdown") {
		t.Fatal("a graceful stop must ask the guest to shut down")
	}

	immediate, immediateRunner := scriptedLibvirt(t, nil)
	if err := immediate.Stop(context.Background(), ref, 0); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !ranSubcommand(immediateRunner, "destroy") {
		t.Fatal("a zero grace must power the guest off")
	}
}

// ranSubcommand reports whether the fake observed a virsh subcommand.
func ranSubcommand(runner *FakeRunner, name string) bool {
	for _, cmd := range runner.Commands() {
		if subcommand(cmd.Args) == name {
			return true
		}
	}
	return false
}

// TestLibvirtSnapshotArgv pins the snapshot and restore invocations.
func TestLibvirtSnapshotArgv(t *testing.T) {
	backend, runner := scriptedLibvirt(t, nil)
	ref := Ref{Class: ClassTenant, TenantID: "acme", Name: "db"}

	if _, err := backend.Snapshot(context.Background(), ref, "nightly-01"); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := backend.Restore(context.Background(), ref, "nightly-01"); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	var joined []string
	for _, cmd := range runner.Commands() {
		joined = append(joined, strings.Join(cmd.Args[2:], " "))
	}
	all := strings.Join(joined, "|")
	if !strings.Contains(all, "snapshot-create-as --domain cashp-t-acme--db --name nightly-01") {
		t.Fatalf("unexpected snapshot argv: %v", joined)
	}
	if !strings.Contains(all, "snapshot-revert --domain cashp-t-acme--db --snapshotname nightly-01") {
		t.Fatalf("unexpected restore argv: %v", joined)
	}

	if _, err := backend.Snapshot(context.Background(), ref, "bad; rm -rf /"); !IsValidation(err) {
		t.Fatalf("an unsafe snapshot name was accepted: %v", err)
	}
}

// TestLibvirtUnsupportedOperations checks that the operations a hypervisor
// cannot express fail loudly rather than silently doing nothing.
func TestLibvirtUnsupportedOperations(t *testing.T) {
	backend, _ := scriptedLibvirt(t, nil)
	ref := Ref{Class: ClassTenant, TenantID: "acme", Name: "db"}
	ctx := context.Background()

	if _, err := backend.Logs(ctx, ref, LogOptions{Tail: 10}); !IsUnsupported(err) {
		t.Fatalf("Logs must be unsupported, got %v", err)
	}
	if _, err := backend.Exec(ctx, ref, ExecRequest{Argv: []string{"id"}}); !IsUnsupported(err) {
		t.Fatalf("Exec must be unsupported, got %v", err)
	}
	if _, err := backend.PullImage(ctx, ImageRequest{Reference: "alpine"}); !IsUnsupported(err) {
		t.Fatalf("PullImage must be unsupported, got %v", err)
	}

	caps := backend.Capabilities()
	for _, absent := range []Capability{CapLogs, CapExec, CapImagePull, CapPortMapping, CapVolumeMount} {
		if caps.Has(absent) {
			t.Fatalf("libvirt must not claim %q", absent)
		}
	}
	for _, present := range []Capability{CapCreate, CapSnapshot, CapDiskAttach} {
		if !caps.Has(present) {
			t.Fatalf("libvirt must claim %q", present)
		}
	}
}

// TestLibvirtCreateRefusesCommandOverride checks that a spec asking for an
// entrypoint override on a guest is refused instead of being dropped.
func TestLibvirtCreateRefusesCommandOverride(t *testing.T) {
	backend, _ := scriptedLibvirt(t, nil)

	spec := vmSpec()
	spec.Command = []string{"/bin/sh"}
	resolved, err := testConfig().resolveSpec(spec)
	if err != nil {
		t.Fatalf("resolveSpec: %v", err)
	}
	if _, err := backend.Create(context.Background(), resolved); !IsUnsupported(err) {
		t.Fatalf("expected an unsupported-operation error, got %v", err)
	}
}

// TestLibvirtInspectRefusesForeignOwner is the IDOR check at the backend
// boundary: a domain whose definition names another account is never returned.
func TestLibvirtInspectRefusesForeignOwner(t *testing.T) {
	foreign := vmSpec()
	foreign.Ref.TenantID = "other"
	resolved, err := testConfig().resolveSpec(foreign)
	if err != nil {
		t.Fatalf("resolveSpec: %v", err)
	}
	resolved.NetworkName = DefaultLibvirtNetwork
	definition, err := buildDomainXML(testConfig(), resolved)
	if err != nil {
		t.Fatalf("buildDomainXML: %v", err)
	}

	runner := &FakeRunner{Respond: func(bin string, args []string, stdin []byte) (RunResult, error) {
		if subcommand(args) == "dumpxml" {
			return RunResult{Stdout: definition}, nil
		}
		return RunResult{Stdout: []byte("running\n")}, nil
	}}
	backend, err := NewLibvirtBackend(testConfig(), runner)
	if err != nil {
		t.Fatalf("NewLibvirtBackend: %v", err)
	}

	ref := Ref{Class: ClassTenant, TenantID: "acme", Name: "db"}
	if _, err := backend.Inspect(context.Background(), ref); !IsTenantMismatch(err) {
		t.Fatalf("expected a tenant mismatch, got %v", err)
	}
}

// TestLibvirtMapsMissingDomain checks that a hypervisor error is classified
// without carrying the raw stderr, which names the connection URI.
func TestLibvirtMapsMissingDomain(t *testing.T) {
	backend, _ := scriptedLibvirt(t, func(args []string) (RunResult, bool) {
		if subcommand(args) == "dumpxml" {
			return RunResult{
				ExitCode: 1,
				Stderr:   []byte("error: failed to get domain 'x': Domain not found: qemu:///system"),
			}, true
		}
		return RunResult{}, false
	})

	ref := Ref{Class: ClassTenant, TenantID: "acme", Name: "db"}
	_, err := backend.Inspect(context.Background(), ref)
	if !IsNotFound(err) {
		t.Fatalf("expected a not-found error, got %v", err)
	}
	if strings.Contains(err.Error(), "qemu:///system") {
		t.Fatalf("the connection URI leaked into the error: %v", err)
	}
}

// TestLibvirtProbeReportsVersion checks the availability probe.
func TestLibvirtProbeReportsVersion(t *testing.T) {
	backend, _ := scriptedLibvirt(t, func(args []string) (RunResult, bool) {
		if subcommand(args) == "version" {
			return RunResult{Stdout: []byte(
				"Compiled against library: libvirt 9.0.0\n" +
					"Using library: libvirt 9.0.0\n" +
					"Using API: QEMU 9.0.0\n" +
					"Running hypervisor: QEMU 8.2.2\n")}, true
		}
		return RunResult{}, false
	})

	status, err := backend.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !status.Available || status.Name != BackendLibvirt {
		t.Fatalf("unexpected status %#v", status)
	}
	if status.Version != "QEMU 8.2.2" {
		t.Fatalf("unexpected version %q", status.Version)
	}
	if len(status.Kinds) != 1 || status.Kinds[0] != KindVM {
		t.Fatalf("libvirt manages virtual machines only, got %#v", status.Kinds)
	}
}

// TestLibvirtProbeFailsWhenHypervisorIsDown checks the unreachable path.
func TestLibvirtProbeFailsWhenHypervisorIsDown(t *testing.T) {
	backend, _ := scriptedLibvirt(t, func(args []string) (RunResult, bool) {
		return RunResult{
			ExitCode: 1,
			Stderr:   []byte("error: failed to connect to the hypervisor"),
		}, true
	})

	status, err := backend.Probe(context.Background())
	if !IsUnavailable(err) {
		t.Fatalf("expected an unavailable error, got %v", err)
	}
	if status.Available {
		t.Fatal("a failed probe must not report the backend as available")
	}
}

// TestLibvirtListFiltersToOneAccount checks that the domain list is narrowed
// by the qualified-name scheme and never reports a foreign or hand-made guest.
func TestLibvirtListFiltersToOneAccount(t *testing.T) {
	backend, _ := scriptedLibvirt(t, func(args []string) (RunResult, bool) {
		if subcommand(args) == "list" {
			return RunResult{Stdout: []byte(
				"cashp-t-acme--db\ncashp-t-other--db\nhandmade-vm\n\n")}, true
		}
		return RunResult{}, false
	})

	instances, err := backend.List(context.Background(), Filter{TenantID: "acme"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("expected exactly one instance, got %#v", instances)
	}
	if instances[0].Ref.TenantID != "acme" || instances[0].QualifiedName != "cashp-t-acme--db" {
		t.Fatalf("unexpected instance %#v", instances[0])
	}

	empty, err := backend.List(context.Background(), Filter{TenantID: "acme", Kind: KindContainer})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("libvirt must report no containers, got %#v", empty)
	}
}

// TestLibvirtHonoursCancelledContext checks that a caller's deadline stops
// the operation instead of running an unbounded command.
func TestLibvirtHonoursCancelledContext(t *testing.T) {
	backend, runner := scriptedLibvirt(t, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := backend.Probe(ctx); err == nil {
		t.Fatal("a cancelled context must fail the probe")
	}
	if len(runner.Commands()) != 1 {
		t.Fatalf("exactly one command should have been attempted, got %d", len(runner.Commands()))
	}
}

// TestNewLibvirtBackendRejectsUnsafeBinary keeps a misconfigured client name
// from reaching the process runner.
func TestNewLibvirtBackendRejectsUnsafeBinary(t *testing.T) {
	cfg := testConfig()
	cfg.VirshBinary = "virsh; id"
	if _, err := NewLibvirtBackend(cfg, &FakeRunner{}); !IsValidation(err) {
		t.Fatalf("expected a validation error, got %v", err)
	}
}
