package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// containerMarkerFiles are files that only exist inside a container.
var containerMarkerFiles = []string{
	// Docker
	"/.dockerenv",
	// Podman
	"/run/.containerenv",
	// LXC/LXD/Incus
	"/dev/lxc",
}

// containerInitProcesses are the init shims container images use as PID 1.
var containerInitProcesses = map[string]bool{
	"tini":      true,
	"dumb-init": true,
	"s6-svscan": true,
	"runsv":     true,
	"runsvdir":  true,
	"catatonit": true,
	// Our own binary as the parent means we are the container entrypoint.
	Name: true,
}

// inContainer is the indirection every caller inside this package uses, so
// tests can exercise both the container and the native branch.
var inContainer = IsContainer

// IsContainer reports whether the process runs inside a container. When it
// is true no PID file is written at all: the runtime supervises the
// process and PIDs are namespace-local, so a PID file on a mounted volume
// would name the wrong process when read from the host.
func IsContainer() bool {
	return detectContainer(containerMarkerFiles, os.Getenv, os.ReadFile, parentProcessName)
}

// detectContainer holds the detection logic with its inputs injected so it
// can be exercised without a real container.
func detectContainer(
	markerFiles []string,
	getenv func(string) string,
	readFile func(string) ([]byte, error),
	parentName func() string,
) bool {
	for _, f := range markerFiles {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}

	// systemd-nspawn, lxc, and podman all export this.
	if getenv("container") != "" {
		return true
	}
	if getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}

	if containerInitProcesses[parentName()] {
		return true
	}

	if data, err := readFile("/proc/1/cgroup"); err == nil {
		content := string(data)
		if strings.Contains(content, "docker") ||
			strings.Contains(content, "kubepods") ||
			strings.Contains(content, "lxc") {
			return true
		}
	}

	return false
}

// parentProcessName returns the command name of the parent process, or an
// empty string when it cannot be determined.
func parentProcessName() string {
	ppid := os.Getppid()
	if ppid <= 0 {
		return ""
	}

	if data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(ppid), "comm")); err == nil {
		return strings.TrimSpace(string(data))
	}

	output, err := exec.Command("ps", "-p", strconv.Itoa(ppid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	return filepath.Base(strings.TrimSpace(string(output)))
}
