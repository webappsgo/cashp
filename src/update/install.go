package update

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
	"strings"

	"github.com/webappsgo/cashp/src/notify"
)

// maxFeedBytes caps how much of an API or checksum response is read, so a
// hostile or broken endpoint cannot exhaust memory.
const maxFeedBytes = 8 << 20

// maxBinaryBytes caps a downloaded release asset at 512 MiB.
const maxBinaryBytes int64 = 512 << 20

// backupSuffix names the retained copy of the replaced binary. It is what
// Rollback restores from, and on Windows it is also the only way to move a
// running executable out of the way.
const backupSuffix = ".old"

// defaultBinaryMode is used when the target binary does not exist yet and
// therefore has no mode to preserve.
const defaultBinaryMode os.FileMode = 0o755

// ErrNoBackup is returned by Rollback when no previous binary was retained.
var ErrNoBackup = errors.New("update: no previous binary retained for rollback")

// Install downloads the release asset for this platform, verifies it
// against the release's sha256.txt, and replaces the target binary
// atomically, keeping the previous binary for Rollback. This is the
// verified download and replacement described in AI.md PART 23 "Update
// Flow" steps 2-4; the installed binary is not touched until verification
// has passed.
func (s *Service) Install(ctx context.Context, r Release) error {
	target, err := s.targetPath()
	if err != nil {
		return err
	}

	assetName := assetNameFor(target)

	var downloadURL, checksumURL string
	for _, a := range r.Assets {
		if a.Name == assetName {
			downloadURL = a.URL
		}
		if a.Name == checksumAsset {
			checksumURL = a.URL
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("update: no %s asset in release %s", assetName, r.Version)
	}
	// Checksum verification is mandatory; an unverifiable release is
	// refused rather than installed.
	if checksumURL == "" {
		return fmt.Errorf("update: release %s has no %s asset - refusing unverified update", r.Version, checksumAsset)
	}

	expected, err := s.fetchChecksum(ctx, checksumURL, assetName)
	if err != nil {
		return err
	}

	tmpPath, err := s.download(ctx, downloadURL, target, expected)
	if err != nil {
		return err
	}

	if err := replaceBinary(target, tmpPath); err != nil {
		_ = os.Remove(tmpPath)

		return err
	}

	s.notify(ctx, notify.EventUpdateInstalled, map[string]string{
		"previous_version": s.opts.CurrentVersion,
		"new_version":      r.Version,
	})

	return nil
}

// Rollback restores the binary retained by the last successful Install.
func (s *Service) Rollback(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	target, err := s.targetPath()
	if err != nil {
		return err
	}

	backup := target + backupSuffix
	info, err := os.Stat(backup)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNoBackup
		}

		return err
	}

	if err := os.Rename(backup, target); err != nil {
		// Windows refuses to rename over an existing file, so the current
		// binary is removed first and the rename retried.
		if rmErr := os.Remove(target); rmErr != nil {
			return fmt.Errorf("update: rollback failed: %w", err)
		}
		if err := os.Rename(backup, target); err != nil {
			return fmt.Errorf("update: rollback failed: %w", err)
		}
	}

	if err := os.Chmod(target, info.Mode().Perm()); err != nil {
		return fmt.Errorf("update: restore permissions: %w", err)
	}

	return nil
}

// targetPath resolves the binary this Service updates, defaulting to the
// running executable with any symlinks resolved.
func (s *Service) targetPath() (string, error) {
	path := s.opts.BinaryPath
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("update: locate executable: %w", err)
		}
		path = exe
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return filepath.Clean(path), nil
		}

		return "", fmt.Errorf("update: resolve symlinks: %w", err)
	}

	return resolved, nil
}

// assetNameFor derives the release asset name for the given binary on this
// platform, so cashp, cashp-cli and cashp-agent each fetch their own
// artifact from the same release.
func assetNameFor(target string) string {
	base := strings.TrimSuffix(filepath.Base(target), ".exe")
	name := base + "-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	return name
}

// download streams the asset into a temporary file alongside the target,
// hashing as it writes, and deletes the file unless the digest matches the
// expected value.
func (s *Service) download(ctx context.Context, url, target, expected string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update: download failed: %d", resp.StatusCode)
	}

	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+"-update-*")
	if err != nil {
		return "", fmt.Errorf("update: create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, digest), io.LimitReader(resp.Body, maxBinaryBytes)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)

		return "", fmt.Errorf("update: download: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)

		return "", fmt.Errorf("update: sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)

		return "", fmt.Errorf("update: close temp file: %w", err)
	}

	actual := hex.EncodeToString(digest.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		_ = os.Remove(tmpPath)

		return "", fmt.Errorf("update: checksum mismatch: expected %s, got %s", expected, actual)
	}

	if err := os.Chmod(tmpPath, targetMode(target)); err != nil {
		_ = os.Remove(tmpPath)

		return "", fmt.Errorf("update: set permissions: %w", err)
	}

	return tmpPath, nil
}

// targetMode returns the mode to give the replacement binary, preserving
// the mode of the binary being replaced when it already exists.
func targetMode(target string) os.FileMode {
	if info, err := os.Stat(target); err == nil {
		if mode := info.Mode().Perm(); mode != 0 {
			return mode
		}
	}

	return defaultBinaryMode
}

// replaceBinary swaps newPath into place at target, retaining the previous
// binary as target+backupSuffix. Moving the old file aside first is what
// makes this work on Windows, where a running executable cannot be
// overwritten, and it is what gives Rollback something to restore.
func replaceBinary(target, newPath string) error {
	backup := target + backupSuffix
	_ = os.Remove(backup)

	replaced := false
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("update: move current binary aside: %w", err)
		}
		replaced = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("update: stat current binary: %w", err)
	}

	if err := os.Rename(newPath, target); err != nil {
		if replaced {
			_ = os.Rename(backup, target)
		}

		return fmt.Errorf("update: install new binary: %w", err)
	}

	syncDir(filepath.Dir(target))

	return nil
}

// syncDir flushes the directory entry so the completed rename survives a
// crash. Directory fsync is unsupported on some platforms and filesystems,
// and a refusal there does not invalidate the rename, so failures are not
// propagated.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()

	_ = d.Sync()
}

// fetchChecksum downloads the release's sha256.txt and returns the hex
// digest recorded for assetName. The file uses standard sha256sum output,
// "{hash}  {filename}" per line.
func (s *Service) fetchChecksum(ctx context.Context, url, assetName string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update: checksum download failed: %d", resp.StatusCode)
	}

	body, err := readCapped(resp.Body, 1<<20)
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == assetName {
			return fields[0], nil
		}
	}

	return "", fmt.Errorf("update: no checksum entry for %s in %s", assetName, checksumAsset)
}

// readCapped reads at most limit bytes from r.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit))
}
