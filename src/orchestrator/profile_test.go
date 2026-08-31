package orchestrator

import (
	"strings"
	"testing"
)

// testConfig is the configuration the isolation tests resolve specs against.
func testConfig() Config {
	return Config{
		TenantVolumeRoot: "/srv/cashp/tenants",
		AppDataRoots:     []string{"/srv/cashp/appdata"},
	}
}

// tenantSpec is a minimal, valid tenant container spec.
func tenantSpec() Spec {
	return Spec{
		Ref:   Ref{Class: ClassTenant, TenantID: "acme", Name: "web"},
		Kind:  KindContainer,
		Image: "alpine:3.19",
	}
}

// TestTenantProfileIsClosedByDefault pins the isolation profile a tenant
// workload runs under.
func TestTenantProfileIsClosedByDefault(t *testing.T) {
	p := TenantProfile()
	if p.AllowPrivileged || p.AllowHostNetwork || p.AllowHostPaths {
		t.Fatalf("tenant profile is not closed: %#v", p)
	}
	if !p.NoNewPrivileges {
		t.Fatal("tenant workloads must run with no-new-privileges")
	}
	if len(p.DropCapabilities) == 0 || p.DropCapabilities[0] != "ALL" {
		t.Fatalf("tenant workloads must drop all capabilities, got %#v", p.DropCapabilities)
	}
	if p.DefaultPidsLimit <= 0 {
		t.Fatal("tenant workloads must carry a default pids limit")
	}
}

// TestProfileForRefusesNativeClass keeps native host services out of the
// orchestrator: they are the service supervisor's responsibility.
func TestProfileForRefusesNativeClass(t *testing.T) {
	if _, err := ProfileFor(ClassNative); !IsIsolationViolation(err) {
		t.Fatalf("expected an isolation violation, got %v", err)
	}
}

// TestResolveSpecRefusesPrivilegedTenantWorkload is the core isolation check.
func TestResolveSpecRefusesPrivilegedTenantWorkload(t *testing.T) {
	spec := tenantSpec()
	spec.Privileged = true
	if _, err := testConfig().resolveSpec(spec); !IsIsolationViolation(err) {
		t.Fatalf("expected an isolation violation, got %v", err)
	}
}

// TestResolveSpecRefusesHostNetworkForTenant keeps a tenant off the host
// network namespace.
func TestResolveSpecRefusesHostNetworkForTenant(t *testing.T) {
	spec := tenantSpec()
	spec.Network = Network{Mode: NetworkHost}
	if _, err := testConfig().resolveSpec(spec); !IsIsolationViolation(err) {
		t.Fatalf("expected an isolation violation, got %v", err)
	}
}

// TestResolveSpecRefusesEngineSocketMount is the check that stops the classic
// container escape.
func TestResolveSpecRefusesEngineSocketMount(t *testing.T) {
	for _, source := range []string{"/var/run/docker.sock", "/run/podman/podman.sock", "/var/run/incus/unix.socket"} {
		spec := tenantSpec()
		spec.Volumes = []VolumeMount{{Source: source, Target: "/sock"}}
		if _, err := testConfig().resolveSpec(spec); err == nil {
			t.Fatalf("mounting %q was accepted", source)
		}
	}
}

// TestResolveSpecRefusesAbsoluteTenantVolume keeps a tenant from naming any
// host path at all.
func TestResolveSpecRefusesAbsoluteTenantVolume(t *testing.T) {
	spec := tenantSpec()
	spec.Volumes = []VolumeMount{{Source: "/etc", Target: "/host-etc"}}
	if _, err := testConfig().resolveSpec(spec); !IsIsolationViolation(err) {
		t.Fatalf("expected an isolation violation, got %v", err)
	}
}

// TestResolveSpecConfinesTenantVolume checks that a relative source lands
// under the account's own storage root.
func TestResolveSpecConfinesTenantVolume(t *testing.T) {
	spec := tenantSpec()
	spec.Volumes = []VolumeMount{{Source: "data", Target: "/data"}}

	resolved, err := testConfig().resolveSpec(spec)
	if err != nil {
		t.Fatalf("resolveSpec: %v", err)
	}
	if len(resolved.Mounts) != 1 {
		t.Fatalf("expected one mount, got %d", len(resolved.Mounts))
	}
	want := "/srv/cashp/tenants/acme/data"
	if resolved.Mounts[0].HostPath != want {
		t.Fatalf("mount resolved to %q, want %q", resolved.Mounts[0].HostPath, want)
	}
}

// TestResolveSpecRefusesVolumeEscape checks that a traversal in a relative
// source cannot walk out of the account's storage root.
func TestResolveSpecRefusesVolumeEscape(t *testing.T) {
	for _, source := range []string{"../other/data", "../../etc", "data/../../../etc"} {
		spec := tenantSpec()
		spec.Volumes = []VolumeMount{{Source: source, Target: "/data"}}
		if _, err := testConfig().resolveSpec(spec); err == nil {
			t.Fatalf("source %q escaped the tenant root", source)
		}
	}
}

// TestResolveSpecAppManagedUsesSystemNamespace checks that an app-managed
// container gets the documented, distinct profile rather than a tenant one.
func TestResolveSpecAppManagedUsesSystemNamespace(t *testing.T) {
	spec := Spec{
		Ref:     Ref{Class: ClassAppManaged, TenantID: SystemTenantID, Name: "mail"},
		Kind:    KindContainer,
		Image:   "alpine:3.19",
		Volumes: []VolumeMount{{Source: "/srv/cashp/appdata/mail", Target: "/data"}},
	}
	resolved, err := testConfig().resolveSpec(spec)
	if err != nil {
		t.Fatalf("resolveSpec: %v", err)
	}
	if !resolved.Profile.AllowHostPaths {
		t.Fatal("the app-managed profile must allow its configured data roots")
	}
	if resolved.Profile.AllowPrivileged || resolved.Profile.AllowHostNetwork {
		t.Fatal("even app-managed containers are not privileged and not on the host network")
	}
	if resolved.Mounts[0].HostPath != "/srv/cashp/appdata/mail" {
		t.Fatalf("unexpected mount %q", resolved.Mounts[0].HostPath)
	}
}

// TestResolveSpecAppManagedRefusesForeignHostPath keeps even the platform's
// own containers inside the configured data roots.
func TestResolveSpecAppManagedRefusesForeignHostPath(t *testing.T) {
	spec := Spec{
		Ref:     Ref{Class: ClassAppManaged, TenantID: SystemTenantID, Name: "mail"},
		Kind:    KindContainer,
		Image:   "alpine:3.19",
		Volumes: []VolumeMount{{Source: "/srv/elsewhere", Target: "/data"}},
	}
	if _, err := testConfig().resolveSpec(spec); !IsIsolationViolation(err) {
		t.Fatalf("expected an isolation violation, got %v", err)
	}
}

// TestResolveSpecLabelsCarryOwnership checks that every workload is stamped
// with the account it belongs to.
func TestResolveSpecLabelsCarryOwnership(t *testing.T) {
	resolved, err := testConfig().resolveSpec(tenantSpec())
	if err != nil {
		t.Fatalf("resolveSpec: %v", err)
	}
	if resolved.Labels[LabelManaged] != "true" {
		t.Fatalf("missing managed label: %#v", resolved.Labels)
	}
	if resolved.Labels[LabelTenant] != "acme" {
		t.Fatalf("missing tenant label: %#v", resolved.Labels)
	}
	if resolved.Labels[LabelClass] != string(ClassTenant) {
		t.Fatalf("missing class label: %#v", resolved.Labels)
	}
	if !strings.HasPrefix(resolved.Qualified, namePrefix+"-") {
		t.Fatalf("qualified name %q is outside the managed namespace", resolved.Qualified)
	}
}

// TestResolveSpecRejectsMismatchedTenantAndName rejects a reference that does
// not name an account at all.
func TestResolveSpecRejectsMismatchedTenantAndName(t *testing.T) {
	spec := tenantSpec()
	spec.Ref.TenantID = ""
	if _, err := testConfig().resolveSpec(spec); !IsValidation(err) {
		t.Fatalf("expected a validation error, got %v", err)
	}
}

// TestResolveSpecWithoutVolumeRootRefusesVolumes keeps a misconfigured node
// from defaulting a tenant volume onto an arbitrary host directory.
func TestResolveSpecWithoutVolumeRootRefusesVolumes(t *testing.T) {
	spec := tenantSpec()
	spec.Volumes = []VolumeMount{{Source: "data", Target: "/data"}}
	if _, err := (Config{}).resolveSpec(spec); !IsValidation(err) {
		t.Fatalf("expected a validation error, got %v", err)
	}
}
