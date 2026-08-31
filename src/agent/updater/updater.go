// Package updater implements the agent's self-update: check the version
// the panel publishes, download it from the panel, verify the SHA-256 the
// panel declared, and swap the binary in atomically. A download whose
// digest does not match is discarded — the agent never executes an
// unverified binary.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/webappsgo/cashp/src/agent/transport"
	"github.com/webappsgo/cashp/src/client/urlutil"
	"github.com/webappsgo/cashp/src/config"
)

// MaxUpdateBytes caps the download size of an agent update.
const MaxUpdateBytes int64 = 256 << 20

// BinaryPathTemplate is the panel route serving agent builds, mirroring the
// CLI's /cli/binaries/{name}.
const BinaryPathTemplate = "/agent/binaries/{name}"

// BinaryPerm is the mode an installed agent binary carries.
const BinaryPerm = 0o755

// Mode values accepted by --update.
const (
	ModeCheck   = "check"
	ModeConfirm = "yes"
)

// ErrNoBuild is returned when the panel publishes nothing for this platform.
var ErrNoBuild = errors.New("the server publishes no agent build for this platform")

// ErrChecksumMismatch is returned when a download does not match the digest
// the panel declared.
var ErrChecksumMismatch = errors.New("update checksum mismatch; the download was discarded")

// ErrNotWritable is returned when the installed binary cannot be replaced.
var ErrNotWritable = errors.New("no permission to replace the installed agent binary")

// Result describes the outcome of an update attempt.
type Result struct {
	// Current is the version that was running.
	Current string
	// Available is the version the panel publishes, if any.
	Available string
	// Updated reports whether the binary was actually replaced.
	Updated bool
	// Path is the binary that was replaced, when Updated is true.
	Path string
}

// Message renders the result as one line for terminal output.
func (r Result) Message() string {
	switch {
	case r.Updated:
		return fmt.Sprintf("updated %s-agent to %s", config.InternalName, r.Available)
	case r.Available == "" || r.Available == r.Current:
		return fmt.Sprintf("%s-agent %s is up to date", config.InternalName, r.Current)
	default:
		return fmt.Sprintf("update available: %s -> %s", r.Current, r.Available)
	}
}

// Platform is the autodiscover key for this build, e.g. "linux-amd64".
func Platform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// BinaryName is the published artifact filename for this platform.
func BinaryName() string {
	name := config.InternalName + "-agent-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// Options configures an update run.
type Options struct {
	// Client is the authenticated transport used for discovery and download.
	Client *transport.Client
	// Version is the version currently running.
	Version string
	// Mode is "check" to report only, or "yes" to install without asking.
	Mode string
	// HTTPClient downloads the binary. It is separate from the transport's
	// client because the download is a plain byte stream, not an envelope.
	HTTPClient *http.Client
}

// Run performs the documented `--update [check|yes]` flow.
func Run(ctx context.Context, opts Options) (Result, error) {
	if opts.Client == nil {
		return Result{}, errors.New("update needs a transport client")
	}

	current := strings.TrimSpace(opts.Version)
	if current == "" {
		current = "devel"
	}
	result := Result{Current: current}

	cluster, err := opts.Client.Autodiscover(ctx)
	if err != nil {
		return result, err
	}

	build, ok := cluster.BuildFor(Platform())
	if !ok {
		return result, fmt.Errorf("%w: %s", ErrNoBuild, Platform())
	}
	result.Available = build.Version

	if current != "devel" && CompareVersions(current, build.Version) >= 0 {
		return result, nil
	}
	if strings.EqualFold(strings.TrimSpace(opts.Mode), ModeCheck) {
		return result, nil
	}

	target, err := Install(ctx, opts, build)
	if err != nil {
		return result, err
	}

	result.Updated = true
	result.Path = target
	return result, nil
}

// Install downloads, verifies and swaps in the published build, returning
// the path that was replaced.
func Install(ctx context.Context, opts Options, build transport.Build) (string, error) {
	target, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current binary: %w", err)
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return "", fmt.Errorf("resolve current binary: %w", err)
	}
	if err := checkWritable(target); err != nil {
		return "", err
	}

	expected := strings.TrimSpace(build.SHA256)
	if expected == "" {
		return "", errors.New("the server published no checksum for this build")
	}

	downloadURL := build.URL
	if downloadURL == "" {
		downloadURL = urlutil.BuildAPIURL(opts.Client.ActiveServer(), BinaryPathTemplate,
			map[string]string{"name": BinaryName()}, nil)
	}
	if downloadURL == "" {
		return "", errors.New("could not build the update download URL")
	}

	tempDir, err := stagingDir()
	if err != nil {
		return "", err
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	tempPath := filepath.Join(tempDir, "agent.update.tmp")
	sum, err := download(ctx, opts, downloadURL, tempPath)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(sum, expected) {
		return "", ErrChecksumMismatch
	}

	if err := os.Chmod(tempPath, BinaryPerm); err != nil {
		return "", fmt.Errorf("set update permissions: %w", err)
	}
	if err := ReplaceBinary(tempPath, target); err != nil {
		return "", err
	}
	return target, nil
}

// stagingDir creates the per-run temporary directory the download lands in.
func stagingDir() (string, error) {
	parent := filepath.Join(os.TempDir(), config.InternalOrg)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("create update directory: %w", err)
	}

	dir, err := os.MkdirTemp(parent, config.InternalName+"-agent-*")
	if err != nil {
		return "", fmt.Errorf("create update directory: %w", err)
	}
	return dir, nil
}

// download streams the update to path and returns its SHA-256 digest.
func download(ctx context.Context, opts Options, downloadURL, path string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("build download request: %w", err)
	}
	request.Header.Set("User-Agent", config.InternalName+"-agent/"+opts.Version)

	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("could not download the update: %w", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		return "", errors.New("update download failed with HTTP " + strconv.Itoa(response.StatusCode))
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("create update file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(file, digest), io.LimitReader(response.Body, MaxUpdateBytes)); err != nil {
		return "", fmt.Errorf("write update file: %w", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// checkWritable verifies the install path can be replaced before anything
// is downloaded.
func checkWritable(target string) error {
	probe, err := os.CreateTemp(filepath.Dir(target), "."+config.InternalName+"-agent-update-*")
	if err != nil {
		return ErrNotWritable
	}

	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

// ReplaceBinary swaps the new binary into place, keeping the old one aside
// while the rename happens so a failure leaves a working agent behind.
func ReplaceBinary(newPath, target string) error {
	staged := target + ".new"
	if err := copyFile(newPath, staged); err != nil {
		return err
	}
	if err := os.Chmod(staged, BinaryPerm); err != nil {
		return fmt.Errorf("set binary permissions: %w", err)
	}

	backup := target + ".old"
	_ = os.Remove(backup)
	if err := os.Rename(target, backup); err != nil {
		_ = os.Remove(staged)
		return fmt.Errorf("move current binary aside: %w", err)
	}
	if err := os.Rename(staged, target); err != nil {
		_ = os.Rename(backup, target)
		return fmt.Errorf("install new binary: %w", err)
	}
	_ = os.Remove(backup)
	return nil
}

// copyFile copies a file, creating the destination with 0600 first.
func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open update file: %w", err)
	}
	defer func() {
		_ = in.Close()
	}()

	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create staged binary: %w", err)
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy staged binary: %w", err)
	}
	return out.Sync()
}

// CompareVersions compares dotted numeric versions, ignoring a leading "v"
// and any pre-release suffix. It returns -1, 0 or 1.
func CompareVersions(left, right string) int {
	leftParts := versionParts(left)
	rightParts := versionParts(right)

	count := len(leftParts)
	if len(rightParts) > count {
		count = len(rightParts)
	}
	for index := 0; index < count; index++ {
		leftValue := 0
		if index < len(leftParts) {
			leftValue = leftParts[index]
		}
		rightValue := 0
		if index < len(rightParts) {
			rightValue = rightParts[index]
		}
		switch {
		case leftValue < rightValue:
			return -1
		case leftValue > rightValue:
			return 1
		}
	}
	return 0
}

// versionParts splits a version string into its numeric components.
func versionParts(version string) []int {
	trimmed := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if index := strings.IndexAny(trimmed, "-+"); index >= 0 {
		trimmed = trimmed[:index]
	}
	fields := strings.Split(trimmed, ".")

	parts := make([]int, 0, len(fields))
	for _, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil {
			return parts
		}
		parts = append(parts, value)
	}
	return parts
}

// EnforceMinVersion refuses to continue when the panel declares this agent
// too old to talk to it.
func EnforceMinVersion(current string, cluster *transport.Cluster) error {
	if cluster == nil || strings.TrimSpace(cluster.AgentMin) == "" {
		return nil
	}
	if current == "devel" || CompareVersions(current, cluster.AgentMin) >= 0 {
		return nil
	}
	return fmt.Errorf("this agent is too old; the server requires %s — run '%s-agent --update yes' to upgrade",
		cluster.AgentMin, config.InternalName)
}
