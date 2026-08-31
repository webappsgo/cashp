package guard

import "testing"

// testPolicy is a workload policy rooted at a fixed tenant data directory,
// with no devices and no passthrough, matching a fresh install.
func testPolicy() WorkloadPolicy {
	return DefaultWorkloadPolicy("/var/lib/cashp/tenants")
}

// safeSpec is a container spec that satisfies the full isolation posture.
// Every hostile case below is this spec with exactly one field changed, so
// a test failure names the field that was permitted.
func safeSpec() WorkloadSpec {
	return WorkloadSpec{
		Backend:        BackendDocker,
		TenantID:       "t1",
		Name:           "web",
		Image:          "nginx:1.27-alpine",
		NetworkName:    TenantNetworkName("t1"),
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{noNewPrivileges},
		Mounts:         []Mount{{Source: "/var/lib/cashp/tenants/t1/web", Target: "/srv/app", ReadOnly: true}},
		Env:            map[string]string{"APP_ENV": "production"},
		CPUCores:       1,
		MemoryBytes:    512 << 20,
		PidsLimit:      128,
		StorageBytes:   1 << 30,
		ReadOnlyRootFS: true,
	}
}

func TestValidateWorkloadAcceptsTheSafeBaseline(t *testing.T) {
	if err := ValidateWorkload(safeSpec(), testPolicy()); err != nil {
		t.Fatalf("the safe baseline spec was rejected: %v", err)
	}
}

func TestValidateWorkloadRejectsHostNamespaceEscapes(t *testing.T) {
	cases := map[string]func(*WorkloadSpec){
		"privileged":     func(s *WorkloadSpec) { s.Privileged = true },
		"host network":   func(s *WorkloadSpec) { s.HostNetwork = true },
		"host pid":       func(s *WorkloadSpec) { s.HostPID = true },
		"host ipc":       func(s *WorkloadSpec) { s.HostIPC = true },
		"host uts":       func(s *WorkloadSpec) { s.HostUTS = true },
		"host user ns":   func(s *WorkloadSpec) { s.HostUserNS = true },
		"shared network": func(s *WorkloadSpec) { s.NetworkName = "bridge" },
		"host network name": func(s *WorkloadSpec) {
			s.NetworkName = "host"
		},
		"another tenant network": func(s *WorkloadSpec) {
			s.NetworkName = TenantNetworkName("t2")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			spec := safeSpec()
			mutate(&spec)
			if err := ValidateWorkload(spec, testPolicy()); err == nil {
				t.Fatalf("ValidateWorkload permitted %s", name)
			}
		})
	}
}

func TestValidateWorkloadRequiresCapabilityDropAndRefusesDangerousAdds(t *testing.T) {
	spec := safeSpec()
	spec.CapDrop = nil
	if err := ValidateWorkload(spec, testPolicy()); err == nil {
		t.Fatal("ValidateWorkload permitted a workload that did not drop ALL")
	}

	for _, capability := range []string{
		"SYS_ADMIN",
		"cap_sys_admin",
		" CAP_SYS_ADMIN ",
		"SYS_MODULE",
		"SYS_PTRACE",
		"NET_ADMIN",
		"NET_RAW",
		"DAC_READ_SEARCH",
		"MKNOD",
		"ALL",
		"cap_all",
		"",
	} {
		hostile := safeSpec()
		hostile.CapAdd = []string{capability}
		if err := ValidateWorkload(hostile, testPolicy()); err == nil {
			t.Fatalf("ValidateWorkload permitted capability %q", capability)
		}
	}
}

func TestValidateWorkloadRequiresConfinementOptions(t *testing.T) {
	missing := safeSpec()
	missing.SecurityOpt = nil
	if err := ValidateWorkload(missing, testPolicy()); err == nil {
		t.Fatal("ValidateWorkload permitted a workload without no-new-privileges")
	}

	for _, opt := range []string{
		"seccomp=unconfined",
		"apparmor=unconfined",
		"apparmor:unconfined",
		"seccomp = unconfined",
		"label=disable",
	} {
		hostile := safeSpec()
		hostile.SecurityOpt = []string{noNewPrivileges, opt}
		if err := ValidateWorkload(hostile, testPolicy()); err == nil {
			t.Fatalf("ValidateWorkload permitted security option %q", opt)
		}
	}
}

func TestValidateWorkloadRejectsEngineSocketAndHostPathMounts(t *testing.T) {
	for _, source := range []string{
		"/var/run/docker.sock",
		"/run/docker.sock",
		"/run/podman/podman.sock",
		"/var/lib/incus/unix.socket",
		"/var/run/libvirt/libvirt-sock",
		"/etc",
		"/etc/shadow",
		"/root/.ssh",
		"/proc",
		"/sys/fs/cgroup",
		"/dev",
		"/usr/bin",
		"/var/lib/docker",
		"/var/lib/cashp/tenants/t2/web",
		"/var/lib/cashp/tenants/t1/../t2",
		"/var/lib/cashp/tenants/t1/./web",
		"relative/path",
		"/var/lib/cashp/tenants/t1/web\x00/../../etc",
	} {
		spec := safeSpec()
		spec.Mounts = []Mount{{Source: source, Target: "/srv/app"}}
		if err := ValidateWorkload(spec, testPolicy()); err == nil {
			t.Fatalf("ValidateWorkload permitted a mount of %q", source)
		}
	}

	badTarget := safeSpec()
	badTarget.Mounts = []Mount{{Source: "/var/lib/cashp/tenants/t1/web", Target: "srv/../app"}}
	if err := ValidateWorkload(badTarget, testPolicy()); err == nil {
		t.Fatal("ValidateWorkload permitted an unclean mount target")
	}
}

func TestValidateWorkloadRejectsDevicesAndSysctlsByDefault(t *testing.T) {
	device := safeSpec()
	device.Devices = []string{"/dev/kmsg"}
	if err := ValidateWorkload(device, testPolicy()); err == nil {
		t.Fatal("ValidateWorkload granted a device on a policy that allows none")
	}

	sysctl := safeSpec()
	sysctl.Sysctls = map[string]string{"kernel.shm_rmid_forced": "1"}
	if err := ValidateWorkload(sysctl, testPolicy()); err == nil {
		t.Fatal("ValidateWorkload permitted an unlisted sysctl")
	}
}

func TestValidateWorkloadRequiresBoundedLimits(t *testing.T) {
	cases := map[string]func(*WorkloadSpec){
		"no cpu limit":     func(s *WorkloadSpec) { s.CPUCores = 0 },
		"no memory limit":  func(s *WorkloadSpec) { s.MemoryBytes = 0 },
		"no pids limit":    func(s *WorkloadSpec) { s.PidsLimit = 0 },
		"no storage limit": func(s *WorkloadSpec) { s.StorageBytes = 0 },
		"cpu over ceiling": func(s *WorkloadSpec) { s.CPUCores = 64 },
		"memory over ceiling": func(s *WorkloadSpec) {
			s.MemoryBytes = 1 << 50
		},
		"negative memory": func(s *WorkloadSpec) { s.MemoryBytes = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			spec := safeSpec()
			mutate(&spec)
			if err := ValidateWorkload(spec, testPolicy()); err == nil {
				t.Fatalf("ValidateWorkload permitted %s", name)
			}
		})
	}
}

func TestValidateWorkloadRejectsHostileNamesAndImages(t *testing.T) {
	badName := safeSpec()
	badName.Name = "web;rm -rf /"
	if err := ValidateWorkload(badName, testPolicy()); err == nil {
		t.Fatal("ValidateWorkload permitted a workload name containing a metacharacter")
	}

	badImage := safeSpec()
	badImage.Image = "-v/:/host"
	if err := ValidateWorkload(badImage, testPolicy()); err == nil {
		t.Fatal("ValidateWorkload permitted an image reference shaped like an option")
	}

	badTenant := safeSpec()
	badTenant.TenantID = "../t2"
	badTenant.NetworkName = TenantNetworkName("../t2")
	if err := ValidateWorkload(badTenant, testPolicy()); err == nil {
		t.Fatal("ValidateWorkload permitted a traversal-shaped tenant id")
	}

	badBackend := safeSpec()
	badBackend.Backend = Backend("kubectl")
	if err := ValidateWorkload(badBackend, testPolicy()); err == nil {
		t.Fatal("ValidateWorkload permitted an unsupported backend")
	}

	badEnv := safeSpec()
	badEnv.Env = map[string]string{"LD_PRELOAD": "/srv/app/evil.so"}
	if err := ValidateWorkload(badEnv, testPolicy()); err == nil {
		t.Fatal("ValidateWorkload permitted LD_PRELOAD in the workload environment")
	}
}

// safeVM is a VM spec that satisfies the hypervisor posture.
func safeVM() VMSpec {
	return VMSpec{
		TenantID:      "t1",
		Name:          "build",
		Arch:          "x86-64",
		VCPUs:         2,
		MemoryBytes:   2 << 30,
		DiskBytes:     20 << 30,
		DiskPaths:     []string{"/var/lib/cashp/tenants/t1/build.qcow2"},
		NetworkName:   TenantNetworkName("t1"),
		ConsoleListen: "127.0.0.1",
	}
}

func TestValidateVMAcceptsTheSafeBaseline(t *testing.T) {
	if err := ValidateVM(safeVM(), testPolicy()); err != nil {
		t.Fatalf("the safe baseline VM was rejected: %v", err)
	}
}

func TestValidateVMRejectsHostileConfigurations(t *testing.T) {
	cases := map[string]func(*VMSpec){
		"host bridge":         func(s *VMSpec) { s.HostBridge = true },
		"foreign network":     func(s *VMSpec) { s.NetworkName = TenantNetworkName("t2") },
		"pci passthrough":     func(s *VMSpec) { s.PassthroughDevices = []string{"0000:01:00.0"} },
		"host disk":           func(s *VMSpec) { s.DiskPaths = []string{"/dev/sda"} },
		"foreign tenant disk": func(s *VMSpec) { s.DiskPaths = []string{"/var/lib/cashp/tenants/t2/x.qcow2"} },
		"traversal disk":      func(s *VMSpec) { s.DiskPaths = []string{"/var/lib/cashp/tenants/t1/../t2/x.qcow2"} },
		"wildcard console":    func(s *VMSpec) { s.ConsoleListen = "0.0.0.0" },
		"routable console":    func(s *VMSpec) { s.ConsoleListen = "10.0.0.5" },
		"empty console":       func(s *VMSpec) { s.ConsoleListen = "" },
		"no vcpus":            func(s *VMSpec) { s.VCPUs = 0 },
		"vcpus over ceiling":  func(s *VMSpec) { s.VCPUs = 512 },
		"no disk limit":       func(s *VMSpec) { s.DiskBytes = 0 },
		"hostile name":        func(s *VMSpec) { s.Name = "vm$(id)" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			spec := safeVM()
			mutate(&spec)
			if err := ValidateVM(spec, testPolicy()); err == nil {
				t.Fatalf("ValidateVM permitted %s", name)
			}
		})
	}
}

func TestTenantRootRefusesTraversalAndRelativeRoots(t *testing.T) {
	if _, err := TenantRoot(WorkloadPolicy{TenantDataRoot: "var/lib/cashp"}, "t1"); err == nil {
		t.Fatal("TenantRoot accepted a relative data root")
	}
	if _, err := TenantRoot(testPolicy(), "../../etc"); err == nil {
		t.Fatal("TenantRoot accepted a traversal-shaped tenant id")
	}
	root, err := TenantRoot(testPolicy(), "t1")
	if err != nil {
		t.Fatalf("TenantRoot failed: %v", err)
	}
	if root != "/var/lib/cashp/tenants/t1" {
		t.Fatalf("TenantRoot produced %q", root)
	}
}

func TestDescribeLimitsCarriesNoTenantContent(t *testing.T) {
	spec := safeSpec()
	spec.Name = "secret-name"
	if got := DescribeLimits(spec); got != "cpu=1 mem=536870912 pids=128 storage=1073741824" {
		t.Fatalf("DescribeLimits produced %q", got)
	}
}
