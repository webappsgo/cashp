package nodes

import (
	"strings"
)

// Size and value bounds for everything a node reports. A node is untrusted
// input (IDEA.md -> "Threat model & abuse cases"): every field below is
// validated against an allowlist and bounded before it reaches storage, an
// operator's screen, or any other subsystem.
const (
	// MaxNodeIDLen bounds a node identifier.
	MaxNodeIDLen = 63
	// MinNodeIDLen is the shortest usable node identifier.
	MinNodeIDLen = 2
	// MaxNodeNameLen bounds an operator-facing node name.
	MaxNodeNameLen = 64
	// MaxAddressLen bounds a reported host:port.
	MaxAddressLen = 255
	// MaxVersionLen bounds a reported agent version string.
	MaxVersionLen = 32
	// MaxKernelLen bounds a reported kernel release.
	MaxKernelLen = 64
	// MaxHostnameLen bounds a reported hostname.
	MaxHostnameLen = 253
	// MaxBackends bounds how many backends a node may claim.
	MaxBackends = 32
	// MaxBackendLen bounds one backend identifier.
	MaxBackendLen = 24
	// MaxActionLen bounds a dispatchable action name.
	MaxActionLen = 64
	// MaxReasonLen bounds an operator-supplied state-change reason.
	MaxReasonLen = 200
	// MaxResultLen bounds a node-reported task result.
	MaxResultLen = 4 << 10
	// MaxErrorLen bounds a node-reported task error.
	MaxErrorLen = 1 << 10
	// MaxCPUCores bounds a reported core count.
	MaxCPUCores = 4096
	// MaxMemoryBytes bounds reported RAM at one pebibyte.
	MaxMemoryBytes = int64(1) << 50
	// MaxDiskBytes bounds reported storage at one exbibyte.
	MaxDiskBytes = int64(1) << 60
)

// Facts is the capability and resource inventory a node reports about
// itself. Every field is validated by ValidateFacts before use.
type Facts struct {
	// OS is the operating system family; cashp servers are Linux only
	// (IDEA.md -> "Supported operating systems").
	OS string
	// Arch is the CPU architecture.
	Arch string
	// Kernel is the kernel release string.
	Kernel string
	// Hostname is the node's own hostname.
	Hostname string
	// CPUCores is the usable core count.
	CPUCores int64
	// MemoryBytes is total RAM in bytes.
	MemoryBytes int64
	// DiskBytes is total usable storage in bytes.
	DiskBytes int64
	// Backends lists the container, VM and package backends available on the
	// node. Each entry must be a known backend identifier.
	Backends []string
}

// allowedOS is the operating-system allowlist. IDEA.md restricts the server
// and every managed host to Linux.
var allowedOS = map[string]bool{
	"linux": true,
}

// allowedArch is the CPU architecture allowlist, matching the Go values a
// cashp binary can be built for.
var allowedArch = map[string]bool{
	"amd64":   true,
	"arm64":   true,
	"arm":     true,
	"386":     true,
	"riscv64": true,
	"ppc64le": true,
	"s390x":   true,
}

// allowedBackends is the backend allowlist. It covers the container and VM
// engines IDEA.md keeps first-class, the two init systems cashp manages
// services through, and the OS package managers from the managed-services
// table.
var allowedBackends = map[string]bool{
	"docker":  true,
	"podman":  true,
	"incus":   true,
	"libvirt": true,
	"qemu":    true,
	"systemd": true,
	"openrc":  true,
	"apt":     true,
	"apk":     true,
	"dnf":     true,
	"pacman":  true,
}

// ValidateNodeID enforces the node identifier allowlist: lowercase letters,
// digits, hyphen and dot, starting and ending alphanumeric. The identifier
// reaches log lines, lock names and audit records, so nothing outside this
// set is ever accepted.
func ValidateNodeID(id string) error {
	if len(id) < MinNodeIDLen || len(id) > MaxNodeIDLen {
		return ErrInvalidNodeID
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' || c == '.':
			if i == 0 || i == len(id)-1 {
				return ErrInvalidNodeID
			}
			if id[i-1] == '-' || id[i-1] == '.' {
				return ErrInvalidNodeID
			}
		default:
			return ErrInvalidNodeID
		}
	}
	return nil
}

// ValidateNodeName enforces the display-name allowlist: letters, digits,
// space, hyphen, underscore and dot, with no leading or trailing space.
func ValidateNodeName(name string) error {
	if name == "" || len(name) > MaxNodeNameLen {
		return ErrInvalidNodeName
	}
	if strings.TrimSpace(name) != name {
		return ErrInvalidNodeName
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == ' ' || c == '-' || c == '_' || c == '.':
		default:
			return ErrInvalidNodeName
		}
	}
	return nil
}

// ValidateActionName enforces the action-name allowlist: lowercase letters,
// digits, dot and underscore, in "subsystem.verb" shape.
func ValidateActionName(action string) error {
	if action == "" || len(action) > MaxActionLen {
		return ErrUnknownAction
	}
	dots := 0
	for i := 0; i < len(action); i++ {
		c := action[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
		case c == '.':
			dots++
			if i == 0 || i == len(action)-1 {
				return ErrUnknownAction
			}
		default:
			return ErrUnknownAction
		}
	}
	if dots != 1 {
		return ErrUnknownAction
	}
	return nil
}

// ValidateFacts checks and normalizes a node's self-reported inventory. It
// returns the normalized copy that may be stored; the caller never persists
// the raw input.
func ValidateFacts(f Facts) (Facts, error) {
	// AI.md PART 11 "Defense-in-Depth Layers": reject control chars / null
	// bytes at input — checked against the raw trimmed kernel string before
	// truncate() strips control characters, so a hostile value is rejected
	// outright instead of being silently sanitized and accepted.
	trimmedKernel := strings.TrimSpace(f.Kernel)
	if trimmedKernel == "" || !isPrintableToken(trimmedKernel) {
		return Facts{}, ErrInvalidFacts
	}

	out := Facts{
		OS:          strings.ToLower(strings.TrimSpace(f.OS)),
		Arch:        strings.ToLower(strings.TrimSpace(f.Arch)),
		Kernel:      truncate(f.Kernel, MaxKernelLen),
		Hostname:    strings.ToLower(truncate(f.Hostname, MaxHostnameLen)),
		CPUCores:    f.CPUCores,
		MemoryBytes: f.MemoryBytes,
		DiskBytes:   f.DiskBytes,
	}

	if !allowedOS[out.OS] {
		return Facts{}, ErrInvalidFacts
	}
	if !allowedArch[out.Arch] {
		return Facts{}, ErrInvalidFacts
	}
	if out.Hostname != "" && ValidateNodeID(out.Hostname) != nil {
		return Facts{}, ErrInvalidFacts
	}
	if out.CPUCores < 1 || out.CPUCores > MaxCPUCores {
		return Facts{}, ErrInvalidFacts
	}
	if out.MemoryBytes < 1 || out.MemoryBytes > MaxMemoryBytes {
		return Facts{}, ErrInvalidFacts
	}
	if out.DiskBytes < 0 || out.DiskBytes > MaxDiskBytes {
		return Facts{}, ErrInvalidFacts
	}
	if len(f.Backends) > MaxBackends {
		return Facts{}, ErrInvalidFacts
	}

	seen := make(map[string]bool, len(f.Backends))
	for _, raw := range f.Backends {
		b := strings.ToLower(strings.TrimSpace(raw))
		if len(b) > MaxBackendLen || !allowedBackends[b] {
			return Facts{}, ErrInvalidFacts
		}
		if seen[b] {
			continue
		}
		seen[b] = true
		out.Backends = append(out.Backends, b)
	}
	sortStrings(out.Backends)
	return out, nil
}

// isPrintableToken reports whether s consists only of printable ASCII with
// no space, which is what a kernel release or version string looks like.
func isPrintableToken(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] > '~' {
			return false
		}
	}
	return true
}

// sortStrings orders a small slice in place. The slices here hold at most
// MaxBackends entries, so an insertion sort keeps the dependency surface at
// zero without any measurable cost.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
