package paths

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// noEnv is an environment with nothing set.
func noEnv(string) string { return "" }

// noFile is a filesystem where nothing can be read.
func noFile(string) ([]byte, error) { return nil, errors.New("not found") }

// noParent is a process tree with an unknown parent.
func noParent() string { return "" }

// TestDetectContainerMarkerFile checks the Docker and Podman marker files.
func TestDetectContainerMarkerFile(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, ".dockerenv")
	if err := os.WriteFile(marker, nil, 0644); err != nil {
		t.Fatalf("creating the marker failed: %v", err)
	}

	if !detectContainer([]string{marker}, noEnv, noFile, noParent) {
		t.Error("a marker file must be detected as a container")
	}
	if detectContainer([]string{filepath.Join(dir, "absent")}, noEnv, noFile, noParent) {
		t.Error("a missing marker file must not be detected as a container")
	}
}

// TestDetectContainerEnvironment checks the environment variables set by
// systemd-nspawn, Podman, LXC, and Kubernetes.
func TestDetectContainerEnvironment(t *testing.T) {
	container := func(key string) string {
		if key == "container" {
			return "podman"
		}
		return ""
	}
	if !detectContainer(nil, container, noFile, noParent) {
		t.Error("the container environment variable must be detected")
	}

	kubernetes := func(key string) string {
		if key == "KUBERNETES_SERVICE_HOST" {
			return "10.0.0.1"
		}
		return ""
	}
	if !detectContainer(nil, kubernetes, noFile, noParent) {
		t.Error("the Kubernetes service host must be detected")
	}
}

// TestDetectContainerInitShim checks the init shims used as PID 1 in
// container images, including our own binary as the entrypoint.
func TestDetectContainerInitShim(t *testing.T) {
	for _, shim := range []string{"tini", "dumb-init", "s6-svscan", "runsv", "catatonit", Name} {
		parent := func() string { return shim }
		if !detectContainer(nil, noEnv, noFile, parent) {
			t.Errorf("the %s init shim must be detected as a container", shim)
		}
	}

	parent := func() string { return "bash" }
	if detectContainer(nil, noEnv, noFile, parent) {
		t.Error("a shell parent must not be detected as a container")
	}
}

// TestDetectContainerCgroup checks the PID 1 cgroup inspection.
func TestDetectContainerCgroup(t *testing.T) {
	cases := map[string]bool{
		"0::/docker/9f8c":                  true,
		"0::/kubepods/besteffort/podabc":   true,
		"0::/lxc.payload.mycontainer":      true,
		"0::/user.slice/user-1000.slice/x": false,
	}

	for contents, want := range cases {
		readFile := func(string) ([]byte, error) { return []byte(contents), nil }
		if got := detectContainer(nil, noEnv, readFile, noParent); got != want {
			t.Errorf("cgroup %q detected as container=%v, want %v", contents, got, want)
		}
	}
}

// TestDetectContainerNative checks that a plain host is not a container.
func TestDetectContainerNative(t *testing.T) {
	if detectContainer(nil, noEnv, noFile, noParent) {
		t.Error("a host with no markers must not be detected as a container")
	}
}
