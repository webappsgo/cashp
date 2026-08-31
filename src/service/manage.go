package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// requireElevation gates a privileged service operation. It never prompts:
// when the caller cannot escalate it returns an informative error explaining
// why, and when the caller can escalate it explains how to re-run the
// command (AI.md PART 24 "Overview").
func requireElevation(action string) error {
	if IsElevated() {
		return nil
	}
	ok, reason := CanEscalate()
	if ok {
		return fmt.Errorf("%s requires root privileges: re-run it with sudo, doas or pkexec", action)
	}
	return fmt.Errorf("%s requires root privileges and this account cannot obtain them: %s", action, reason)
}

// confirmUninstall runs the destructive-action confirmation prompt unless
// the caller already confirmed. It is mandatory before an uninstall deletes
// data, config and the system account (AI.md PART 24 "Service Uninstall
// Logic").
func confirmUninstall(confirmed bool) error {
	if confirmed {
		return nil
	}
	ok, err := confirmDestructive(os.Stdin, os.Stdout, confirmPrompt)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotConfirmed
	}
	return nil
}

// purgeState removes the config, data, cache, log, backup and run state, then the
// system account. The binary itself is deliberately left in place and the
// caller is told how to remove it manually.
func purgeState(ctx context.Context, data TemplateData) error {
	for _, dir := range data.StateDirs() {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove %s: %w", dir, err)
		}
	}
	if err := os.Remove(data.PIDFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", data.PIDFile, err)
	}
	if !data.UserMode {
		if err := removeServiceAccount(ctx, ServiceAccountName); err != nil {
			return err
		}
	}
	fmt.Println(uninstallNotice(data.BinaryPath))
	return nil
}

// writeServiceFile writes a generated service definition, creating parent
// directories as needed.
func writeServiceFile(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return os.Chmod(path, mode)
}

// removeServiceFile deletes a generated service definition, tolerating an
// already-missing file so uninstall stays idempotent.
func removeServiceFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// fileExists reports whether a path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// pathExists reports whether a path exists without following symlinks, so a
// dangling link still counts as present.
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// readPIDFile returns the process ID stored in a pid file, or 0 when the
// file is missing, unreadable or does not hold a positive integer.
func readPIDFile(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}
