package orchestrator

import (
	"net"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Identifier length bounds. The ceilings are the strictest of the four
// backends: Incus caps an instance name at 63 characters and accepts only
// letters, digits, and hyphens, so the qualified name this package builds
// must fit inside that even for the longest tenant and workload names.
const (
	// MaxTenantIDLen bounds a tenant identifier.
	MaxTenantIDLen = 24
	// MaxWorkloadNameLen bounds a tenant-visible workload name.
	MaxWorkloadNameLen = 28
	// MaxQualifiedNameLen bounds the derived backend object name.
	MaxQualifiedNameLen = 63
	// MaxImageRefLen bounds an image reference.
	MaxImageRefLen = 255
	// MaxPathLen bounds any host or guest path.
	MaxPathLen = 512
	// MaxArgvEntries bounds the number of argv elements in an exec.
	MaxArgvEntries = 64
	// MaxArgvBytes bounds the total size of an exec argv.
	MaxArgvBytes = 16 * 1024
	// MaxEnvEntries bounds the number of environment variables.
	MaxEnvEntries = 128
	// MaxEnvValueLen bounds one environment value.
	MaxEnvValueLen = 8 * 1024
	// MaxVolumes bounds the number of mounts on one workload.
	MaxVolumes = 32
	// MaxPorts bounds the number of published ports on one workload.
	MaxPorts = 32
)

// namePrefix is stamped on every object this package creates so a
// cashp-managed workload is distinguishable from one an operator created by
// hand on the same engine.
const namePrefix = "cashp"

// dnsLabelRe is the allowlist for tenant identifiers, workload names, and
// network names: lowercase alphanumerics in hyphen-separated groups, never
// starting or ending with a hyphen and never containing a double hyphen.
var dnsLabelRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// imageRefRe is the allowlist for an OCI image reference: an optional
// registry host with an optional port, a lowercase repository path, an
// optional tag, and an optional sha256 digest.
var imageRefRe = regexp.MustCompile(`^(?:([a-zA-Z0-9][a-zA-Z0-9.-]*(?::[0-9]{1,5})?)/)?` +
	`([a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*)` +
	`(?::([A-Za-z0-9_][A-Za-z0-9._-]{0,127}))?` +
	`(?:@(sha256:[a-f0-9]{64}))?$`)

// digestRe is the allowlist for a standalone content digest.
var digestRe = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// envKeyRe is the allowlist for an environment variable name.
var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// snapshotNameRe is the allowlist for a snapshot name. Incus and libvirt
// both accept a wider set, but the intersection is what this package uses.
var snapshotNameRe = regexp.MustCompile(`^[a-z0-9]+([-_][a-z0-9]+)*$`)

// archRe is the allowlist for a guest CPU architecture.
var archRe = regexp.MustCompile(`^[a-z0-9_]+$`)

// diskTargetRe is the allowlist for a guest block device name, e.g. "vda".
var diskTargetRe = regexp.MustCompile(`^[a-z]{2,4}[a-z0-9]?$`)

// shellMetaChars are rejected anywhere in a caller-supplied identifier,
// reference, or path. Nothing in this package builds a shell command line,
// so these characters have no legitimate use in an identifier — rejecting
// them is defence in depth on top of the argv-slice execution model.
const shellMetaChars = "`$&;|<>()[]{}!*?~'\"\\\n\r\t\v\f\x00"

// reservedTenantIDs are identifiers a real tenant may never hold, because
// they would let a tenant workload's derived name collide with the
// app-managed namespace.
var reservedTenantIDs = map[string]bool{
	SystemTenantID: true,
	"system":       true,
	"cashp":        true,
	"root":         true,
	"admin":        true,
}

// deniedPathFragments are substrings that must never appear in a resolved
// host path. Mounting an engine control socket into any workload — tenant
// or app-managed — hands that workload full control of the host, so the
// check is unconditional rather than profile-dependent.
var deniedPathFragments = []string{
	"docker.sock",
	"podman.sock",
	"containerd.sock",
	"crio.sock",
	"libvirt-sock",
	"incus/unix.socket",
	"lxd/unix.socket",
}

// deniedPathPrefixes are host directories no workload may ever mount, even
// under a profile that permits host paths.
var deniedPathPrefixes = []string{
	"/proc",
	"/sys",
	"/dev",
	"/run",
	"/var/run",
	"/boot",
	"/etc/shadow",
	"/root/.ssh",
}

// hasUnsafeChars reports whether s contains a shell metacharacter, any
// whitespace, or a parent-directory reference.
func hasUnsafeChars(s string) bool {
	if strings.ContainsAny(s, shellMetaChars) {
		return true
	}
	if strings.ContainsAny(s, " \t\n\r") {
		return true
	}
	return strings.Contains(s, "..")
}

// ValidateTenantID checks a tenant identifier against the allowlist and
// rejects the reserved identifiers that own the app-managed namespace.
func ValidateTenantID(id string) error {
	if id == "" {
		return validationErr("tenant_id", "required")
	}
	if len(id) > MaxTenantIDLen {
		return validationErr("tenant_id", "too_long")
	}
	if hasUnsafeChars(id) || !dnsLabelRe.MatchString(id) {
		return validationErr("tenant_id", "charset")
	}
	if reservedTenantIDs[id] {
		return validationErr("tenant_id", "reserved")
	}
	return nil
}

// ValidateWorkloadName checks a tenant-visible workload name.
func ValidateWorkloadName(name string) error {
	if name == "" {
		return validationErr("name", "required")
	}
	if len(name) > MaxWorkloadNameLen {
		return validationErr("name", "too_long")
	}
	if hasUnsafeChars(name) || !dnsLabelRe.MatchString(name) {
		return validationErr("name", "charset")
	}
	return nil
}

// ValidateNetworkName checks a per-tenant network or bridge name.
func ValidateNetworkName(name string) error {
	if name == "" {
		return validationErr("network.name", "required")
	}
	if len(name) > MaxQualifiedNameLen {
		return validationErr("network.name", "too_long")
	}
	if hasUnsafeChars(name) || !dnsLabelRe.MatchString(name) {
		return validationErr("network.name", "charset")
	}
	return nil
}

// ValidateImageRef checks an image reference against the OCI grammar
// allowlist. It rejects any reference carrying a shell metacharacter or a
// traversal sequence before the grammar is even applied.
func ValidateImageRef(ref string) error {
	if ref == "" {
		return validationErr("image", "required")
	}
	if len(ref) > MaxImageRefLen {
		return validationErr("image", "too_long")
	}
	if hasUnsafeChars(ref) {
		return validationErr("image", "charset")
	}
	if !imageRefRe.MatchString(ref) {
		return validationErr("image", "format")
	}
	return nil
}

// ValidateDigest checks a standalone content digest.
func ValidateDigest(digest string) error {
	if digest == "" {
		return nil
	}
	if !digestRe.MatchString(digest) {
		return validationErr("image_digest", "format")
	}
	return nil
}

// ValidateSnapshotName checks a snapshot identifier.
func ValidateSnapshotName(name string) error {
	if name == "" {
		return validationErr("snapshot", "required")
	}
	if len(name) > MaxWorkloadNameLen {
		return validationErr("snapshot", "too_long")
	}
	if hasUnsafeChars(name) || !snapshotNameRe.MatchString(name) {
		return validationErr("snapshot", "charset")
	}
	return nil
}

// ValidateGuestPath checks an absolute path inside a workload. It must be
// absolute, already clean, and free of traversal and metacharacters.
func ValidateGuestPath(field, p string) error {
	if p == "" {
		return validationErr(field, "required")
	}
	if len(p) > MaxPathLen {
		return validationErr(field, "too_long")
	}
	if hasUnsafeChars(p) {
		return validationErr(field, "charset")
	}
	if !strings.HasPrefix(p, "/") {
		return validationErr(field, "not_absolute")
	}
	if path.Clean(p) != p {
		return validationErr(field, "not_clean")
	}
	return nil
}

// ValidateHostPath checks an already-resolved absolute host path. It
// enforces the unconditional deny list: no engine control socket and no
// kernel or boot directory may be handed to any workload of any class.
func ValidateHostPath(field, p string) error {
	if p == "" {
		return validationErr(field, "required")
	}
	if len(p) > MaxPathLen {
		return validationErr(field, "too_long")
	}
	if hasUnsafeChars(p) {
		return validationErr(field, "charset")
	}
	if !filepath.IsAbs(p) {
		return validationErr(field, "not_absolute")
	}
	if filepath.Clean(p) != p {
		return validationErr(field, "not_clean")
	}
	lower := strings.ToLower(p)
	for _, frag := range deniedPathFragments {
		if strings.Contains(lower, frag) {
			return validationErr(field, "engine_socket_denied")
		}
	}
	for _, prefix := range deniedPathPrefixes {
		if lower == prefix || strings.HasPrefix(lower, prefix+"/") {
			return validationErr(field, "host_path_denied")
		}
	}
	return nil
}

// ValidateSocketPath checks an engine socket path from operator
// configuration. Operator-supplied configuration is trusted input, but it
// is still shape-checked so a typo cannot turn into a relative path lookup.
func ValidateSocketPath(p string) error {
	if p == "" {
		return validationErr("socket", "required")
	}
	if len(p) > MaxPathLen {
		return validationErr("socket", "too_long")
	}
	if hasUnsafeChars(p) {
		return validationErr("socket", "charset")
	}
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return validationErr("socket", "not_absolute")
	}
	return nil
}

// ValidateBinaryPath checks the absolute path of an external binary this
// package will execute. Only an absolute, clean, metacharacter-free path is
// accepted; the argv slice is built entirely from validated values.
func ValidateBinaryPath(field, p string) error {
	if p == "" {
		return validationErr(field, "required")
	}
	if len(p) > MaxPathLen {
		return validationErr(field, "too_long")
	}
	if hasUnsafeChars(p) {
		return validationErr(field, "charset")
	}
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return validationErr(field, "not_absolute")
	}
	return nil
}

// ValidateEnv checks an environment map. Keys follow the POSIX name
// grammar; values may hold arbitrary text but never a NUL or a newline,
// which would let a value forge a second variable in some encodings.
func ValidateEnv(env map[string]string) error {
	if len(env) > MaxEnvEntries {
		return validationErr("env", "too_many")
	}
	for k, v := range env {
		if !envKeyRe.MatchString(k) {
			return validationErr("env", "key_charset")
		}
		if len(v) > MaxEnvValueLen {
			return validationErr("env", "value_too_long")
		}
		if strings.ContainsAny(v, "\x00\n\r") {
			return validationErr("env", "value_charset")
		}
	}
	return nil
}

// ValidateArgv checks an exec argv slice. A shell is a perfectly legitimate
// argv[0] inside a container — a web terminal needs one — so the check
// bounds size and rejects control bytes rather than second-guessing which
// program a tenant may run inside their own workload.
func ValidateArgv(argv []string) error {
	if len(argv) == 0 {
		return validationErr("argv", "required")
	}
	if len(argv) > MaxArgvEntries {
		return validationErr("argv", "too_many")
	}
	total := 0
	for i, a := range argv {
		if i == 0 && a == "" {
			return validationErr("argv", "empty_command")
		}
		if strings.ContainsRune(a, 0) {
			return validationErr("argv", "null_byte")
		}
		total += len(a)
	}
	if total > MaxArgvBytes {
		return validationErr("argv", "too_large")
	}
	return nil
}

// ValidatePort checks one published port mapping.
func ValidatePort(p PortMapping) error {
	switch strings.ToLower(p.Protocol) {
	case "tcp", "udp", "":
	default:
		return validationErr("ports.protocol", "unsupported")
	}
	if p.TargetPort < 1 || p.TargetPort > 65535 {
		return validationErr("ports.target_port", "range")
	}
	if p.HostPort < 0 || p.HostPort > 65535 {
		return validationErr("ports.host_port", "range")
	}
	if p.HostIP != "" {
		if hasUnsafeChars(p.HostIP) {
			return validationErr("ports.host_ip", "charset")
		}
		if !isIPLiteral(p.HostIP) {
			return validationErr("ports.host_ip", "not_ip")
		}
	}
	return nil
}

// ValidateArchitecture checks a guest CPU architecture token.
func ValidateArchitecture(arch string) error {
	if arch == "" {
		return nil
	}
	if len(arch) > 16 || !archRe.MatchString(arch) {
		return validationErr("architecture", "charset")
	}
	return nil
}

// ValidateDiskTarget checks a guest block device name.
func ValidateDiskTarget(target string) error {
	if target == "" {
		return validationErr("disks.target", "required")
	}
	if !diskTargetRe.MatchString(target) {
		return validationErr("disks.target", "charset")
	}
	return nil
}

// ValidateDiskFormat checks a disk image format against the formats this
// package will write into generated domain XML.
func ValidateDiskFormat(format string) error {
	switch format {
	case "qcow2", "raw":
		return nil
	default:
		return validationErr("disks.format", "unsupported")
	}
}

// ValidateDiskBus checks a guest disk bus.
func ValidateDiskBus(bus string) error {
	switch bus {
	case "virtio", "sata", "scsi", "":
		return nil
	default:
		return validationErr("disks.bus", "unsupported")
	}
}

// Validate checks a Ref end to end: class, tenant, and name.
func (r Ref) Validate() error {
	switch r.Class {
	case ClassAppManaged:
		if r.TenantID != SystemTenantID {
			return validationErr("tenant_id", "app_managed_tenant")
		}
	case ClassTenant:
		if err := ValidateTenantID(r.TenantID); err != nil {
			return err
		}
	case ClassNative:
		return isolationErr(ClassNative, "class",
			"native host services are managed by the service supervisor, not the orchestrator")
	default:
		return validationErr("class", "unknown")
	}
	return ValidateWorkloadName(r.Name)
}

// nameSeparator divides the tenant identifier from the workload name in a
// qualified name. A double hyphen is unambiguous because the allowlist for
// both halves forbids consecutive hyphens, so the split can never land in
// the middle of a tenant identifier that happens to contain a hyphen.
const nameSeparator = "--"

// Qualified returns the backend-visible object name for the ref. The name
// is derived, never caller-supplied, so a tenant cannot name a workload in
// a way that reaches another tenant's object or the app-managed namespace.
func (r Ref) Qualified() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	var name string
	if r.Class == ClassAppManaged {
		name = namePrefix + "-" + SystemTenantID + nameSeparator + r.Name
	} else {
		name = namePrefix + "-t-" + r.TenantID + nameSeparator + r.Name
	}
	if len(name) > MaxQualifiedNameLen {
		return "", validationErr("name", "qualified_too_long")
	}
	return name, nil
}

// parseQualified recovers the ref a qualified name was derived from. It
// returns ok=false for any object this package did not create, which is how
// List avoids reporting engine objects an operator made by hand.
func parseQualified(qualified string) (Ref, bool) {
	rest, ok := strings.CutPrefix(qualified, namePrefix+"-")
	if !ok {
		return Ref{}, false
	}
	if sysName, found := strings.CutPrefix(rest, SystemTenantID+nameSeparator); found {
		if ValidateWorkloadName(sysName) != nil {
			return Ref{}, false
		}
		return Ref{Class: ClassAppManaged, TenantID: SystemTenantID, Name: sysName}, true
	}
	tenantAndName, ok := strings.CutPrefix(rest, "t-")
	if !ok {
		return Ref{}, false
	}
	tenant, name, ok := strings.Cut(tenantAndName, nameSeparator)
	if !ok {
		return Ref{}, false
	}
	if ValidateTenantID(tenant) != nil || ValidateWorkloadName(name) != nil {
		return Ref{}, false
	}
	return Ref{Class: ClassTenant, TenantID: tenant, Name: name}, true
}

// isIPLiteral reports whether s parses as a bare IPv4 or IPv6 address.
func isIPLiteral(s string) bool {
	return net.ParseIP(s) != nil
}
