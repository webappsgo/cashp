package orchestrator

import "testing"

// TestValidateWorkloadNameRejectsInjection covers the names an attacker would
// try to smuggle into an engine route or an argv slice.
func TestValidateWorkloadNameRejectsInjection(t *testing.T) {
	cases := []string{
		"",
		"web; rm -rf /",
		"web && curl evil",
		"web`id`",
		"web$(id)",
		"web|nc",
		"web name",
		"../escape",
		"web\nnext",
		"web\x00null",
		"WEB",
		"-leading",
		"trailing-",
		"this-name-is-far-too-long-for-a-workload-label",
	}
	for _, name := range cases {
		if err := ValidateWorkloadName(name); err == nil {
			t.Fatalf("ValidateWorkloadName(%q) accepted an unsafe name", name)
		}
	}
	if err := ValidateWorkloadName("web-01"); err != nil {
		t.Fatalf("ValidateWorkloadName rejected a valid name: %v", err)
	}
}

// TestValidateImageRefRejectsInjection checks that an image reference cannot
// carry shell syntax or a traversal.
func TestValidateImageRefRejectsInjection(t *testing.T) {
	cases := []string{
		"",
		"alpine:latest; id",
		"alpine latest",
		"../../etc/passwd",
		"alpine:$(id)",
		"alpine:latest\n",
		"registry.example.com/../alpine",
	}
	for _, ref := range cases {
		if err := ValidateImageRef(ref); err == nil {
			t.Fatalf("ValidateImageRef(%q) accepted an unsafe reference", ref)
		}
	}
	for _, ref := range []string{"alpine", "alpine:3.19", "registry.example.com:5000/team/app:1.2.3"} {
		if err := ValidateImageRef(ref); err != nil {
			t.Fatalf("ValidateImageRef(%q) rejected a valid reference: %v", ref, err)
		}
	}
}

// TestValidateDigestFormat checks that only a full sha256 digest is accepted.
func TestValidateDigestFormat(t *testing.T) {
	good := "sha256:" + "ab12cd34" + "ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34"[:56]
	if err := ValidateDigest(good); err != nil {
		t.Fatalf("ValidateDigest rejected a valid digest: %v", err)
	}
	for _, bad := range []string{"sha256:short", "md5:0011", "sha256:ZZ", "sha256:" + "ab"} {
		if err := ValidateDigest(bad); err == nil {
			t.Fatalf("ValidateDigest(%q) accepted an invalid digest", bad)
		}
	}
	if err := ValidateDigest(""); err != nil {
		t.Fatalf("an absent digest must be allowed: %v", err)
	}
}

// TestValidateArgvRejectsUnsafeEntries checks the exec argument allowlist.
func TestValidateArgvRejectsUnsafeEntries(t *testing.T) {
	if err := ValidateArgv(nil); err == nil {
		t.Fatal("an empty argv must be rejected")
	}
	if err := ValidateArgv([]string{"sh", "-c", "id\x00"}); err == nil {
		t.Fatal("a null byte in argv must be rejected")
	}
	if err := ValidateArgv([]string{"echo", "hello world"}); err != nil {
		t.Fatalf("a plain argument must be allowed: %v", err)
	}
}

// TestValidateHostPathRefusesEngineSockets is the check that stops a workload
// from bind-mounting the engine socket it is running under.
func TestValidateHostPathRefusesEngineSockets(t *testing.T) {
	cases := []string{
		"/var/run/docker.sock",
		"/run/podman/podman.sock",
		"/var/run/incus/unix.socket",
		"/proc",
		"/sys/fs/cgroup",
		"/dev/mem",
		"/root/.ssh",
		"relative/path",
		"/etc/../etc/shadow",
	}
	for _, p := range cases {
		if err := ValidateHostPath("volume", p); err == nil {
			t.Fatalf("ValidateHostPath(%q) accepted a denied path", p)
		}
	}
	if err := ValidateHostPath("volume", "/srv/cashp/tenants/acme/data"); err != nil {
		t.Fatalf("ValidateHostPath rejected an ordinary path: %v", err)
	}
}

// TestQualifiedNameRoundTrip checks that the derived engine name encodes the
// account and parses back to the same reference.
func TestQualifiedNameRoundTrip(t *testing.T) {
	ref := Ref{Class: ClassTenant, TenantID: "acme", Name: "web"}
	qualified, err := ref.Qualified()
	if err != nil {
		t.Fatalf("Qualified: %v", err)
	}
	if qualified != "cashp-t-acme--web" {
		t.Fatalf("unexpected qualified name %q", qualified)
	}
	parsed, ok := parseQualified(qualified)
	if !ok || parsed != ref {
		t.Fatalf("parseQualified(%q) = %#v, %v", qualified, parsed, ok)
	}

	sys := Ref{Class: ClassAppManaged, TenantID: SystemTenantID, Name: "mail"}
	sysName, err := sys.Qualified()
	if err != nil {
		t.Fatalf("Qualified: %v", err)
	}
	if sysName != "cashp-sys--mail" {
		t.Fatalf("unexpected system name %q", sysName)
	}
	if parsedSys, ok := parseQualified(sysName); !ok || parsedSys.Class != ClassAppManaged {
		t.Fatalf("parseQualified(%q) = %#v, %v", sysName, parsedSys, ok)
	}
}

// TestParseQualifiedRejectsForeignNames makes sure a container that cashp did
// not create is never attributed to an account.
func TestParseQualifiedRejectsForeignNames(t *testing.T) {
	for _, name := range []string{"nginx", "cashp", "cashp-web", "cashp-t-acme", "cashp-t---web", "other-t-acme--web"} {
		if _, ok := parseQualified(name); ok {
			t.Fatalf("parseQualified(%q) claimed a foreign name", name)
		}
	}
}

// TestValidateTenantIDRejectsReserved keeps an account from claiming the
// namespace the platform's own containers live in.
func TestValidateTenantIDRejectsReserved(t *testing.T) {
	if err := ValidateTenantID(SystemTenantID); err == nil {
		t.Fatal("the system namespace must not be claimable by an account")
	}
	if err := ValidateTenantID("acme"); err != nil {
		t.Fatalf("ValidateTenantID rejected a valid account: %v", err)
	}
}

// TestValidateEnvRejectsUnsafeKeys checks the environment allowlist.
func TestValidateEnvRejectsUnsafeKeys(t *testing.T) {
	if err := ValidateEnv(map[string]string{"BAD KEY": "v"}); err == nil {
		t.Fatal("a key with whitespace must be rejected")
	}
	if err := ValidateEnv(map[string]string{"OK": "value\x00"}); err == nil {
		t.Fatal("a null byte in a value must be rejected")
	}
	if err := ValidateEnv(map[string]string{"APP_ENV": "production"}); err != nil {
		t.Fatalf("a plain environment pair must be allowed: %v", err)
	}
}

// TestValidateSocketPathRejectsNonAbsolute keeps a relative socket path from
// resolving against whatever directory the server happens to run in.
func TestValidateSocketPathRejectsNonAbsolute(t *testing.T) {
	if err := ValidateSocketPath("docker.sock"); err == nil {
		t.Fatal("a relative socket path must be rejected")
	}
	if err := ValidateSocketPath("/var/run/docker.sock"); err != nil {
		t.Fatalf("ValidateSocketPath rejected the default socket: %v", err)
	}
}
