package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/webappsgo/cashp/src/client/api"
	"github.com/webappsgo/cashp/src/client/urlutil"
	"github.com/webappsgo/cashp/src/config"
)

// MaxUpdateBytes caps the download size of a CLI update.
const MaxUpdateBytes int64 = 256 << 20

// UpdateBinaryPathTemplate is the server route serving CLI builds.
const UpdateBinaryPathTemplate = "/cli/binaries/{name}"

// OSArch is the autodiscover key for this build, e.g. "linux-amd64".
func OSArch() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// updateBinaryName is the published artifact filename for this platform.
func updateBinaryName() string {
	name := config.InternalName + "-cli-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// HandleUpdate implements --update, --update check and --update yes.
func HandleUpdate(ctx *Context, mode string) error {
	client, err := ctx.APIClient()
	if err != nil {
		return err
	}

	discovered, err := client.Autodiscover(ctx.Ctx)
	if err != nil {
		return err
	}

	build, ok := discovered.BuildFor(OSArch())
	if !ok {
		return &api.Error{Kind: api.KindNotFound, Message: "the server publishes no CLI build for " + OSArch()}
	}

	if CompareVersions(ctx.Version, build.Version) >= 0 {
		ctx.Out.Message("%s %s is up to date", ctx.BinaryName, ctx.Version)
		return nil
	}

	ctx.Out.Message("update available: %s -> %s", ctx.Version, build.Version)
	if strings.EqualFold(strings.TrimSpace(mode), "check") {
		return nil
	}

	if !ctx.Globals.Yes && !strings.EqualFold(strings.TrimSpace(mode), "yes") && !ctx.Config.Update.Auto {
		confirmed, err := Confirm(ctx, fmt.Sprintf("Install %s %s now?", ctx.BinaryName, build.Version))
		if err != nil {
			return err
		}
		if !confirmed {
			ctx.Out.Message("update skipped")
			return nil
		}
	}

	return installUpdate(ctx, client, build)
}

// EnforceMinVersion refuses to continue when the server declares this CLI
// too old to talk to it.
func EnforceMinVersion(ctx *Context, discovered *api.Autodiscover) error {
	if discovered.CLIMinVer == "" || ctx.Version == "devel" {
		return nil
	}
	if CompareVersions(ctx.Version, discovered.CLIMinVer) >= 0 {
		return nil
	}
	return &api.Error{
		Kind: api.KindConfig,
		Message: fmt.Sprintf("this CLI is too old; the server requires %s — run '%s --update yes' to upgrade",
			discovered.CLIMinVer, ctx.BinaryName),
	}
}

// installUpdate downloads, verifies and swaps in a new binary, then
// re-execs so the interrupted command continues on the new version.
func installUpdate(ctx *Context, client *api.Client, build api.CLIBuild) error {
	target, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current binary: %w", err)
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return fmt.Errorf("resolve current binary: %w", err)
	}

	if err := checkWritable(target); err != nil {
		return err
	}

	tempDir, err := os.MkdirTemp(filepath.Join(os.TempDir(), config.InternalOrg), config.InternalName+"-*")
	if err != nil {
		if mkErr := os.MkdirAll(filepath.Join(os.TempDir(), config.InternalOrg), 0o700); mkErr != nil {
			return fmt.Errorf("create update directory: %w", mkErr)
		}
		tempDir, err = os.MkdirTemp(filepath.Join(os.TempDir(), config.InternalOrg), config.InternalName+"-*")
		if err != nil {
			return fmt.Errorf("create update directory: %w", err)
		}
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	tempPath := filepath.Join(tempDir, "cli.update.tmp")
	downloadURL := build.URL
	if downloadURL == "" {
		downloadURL = urlutil.BuildAPIURL(client.ActiveServer(), UpdateBinaryPathTemplate,
			map[string]string{"name": updateBinaryName()}, nil)
	}
	if downloadURL == "" {
		return &api.Error{Kind: api.KindConfig, Message: "could not build the update download URL"}
	}

	sum, err := download(ctx, downloadURL, tempPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, strings.TrimSpace(build.SHA256)) {
		return &api.Error{Kind: api.KindGeneral, Message: "update checksum mismatch; the download was discarded"}
	}

	if err := os.Chmod(tempPath, 0o755); err != nil {
		return fmt.Errorf("set update permissions: %w", err)
	}
	if err := replaceBinary(tempPath, target); err != nil {
		return err
	}

	ctx.Out.Success("updated %s to %s", ctx.BinaryName, build.Version)
	return reexec(target, ctx.Argv)
}

// download streams the update to path and returns its SHA-256 digest.
func download(ctx *Context, downloadURL, path string) (string, error) {
	request, err := http.NewRequestWithContext(ctx.Ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("build download request: %w", err)
	}
	request.Header.Set("User-Agent", "cashp-cli/"+ctx.Version)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", &api.Error{Kind: api.KindConnection, Message: "could not download the update"}
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		return "", &api.Error{
			Kind:    api.KindGeneral,
			Status:  response.StatusCode,
			Message: "update download failed with HTTP " + strconv.Itoa(response.StatusCode),
		}
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
	directory := filepath.Dir(target)
	probe, err := os.CreateTemp(directory, ".cashp-update-*")
	if err != nil {
		return &api.Error{
			Kind:    api.KindConfig,
			Message: "you do not have permission to update the installed binary; ask your admin or move it to a writable path",
		}
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

// replaceBinary swaps the new binary into place, keeping the old one aside
// while the rename happens so a failure leaves a working CLI behind.
func replaceBinary(newPath, target string) error {
	staged := target + ".new"
	if err := copyFile(newPath, staged); err != nil {
		return err
	}
	if err := os.Chmod(staged, 0o755); err != nil {
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
