package hosting

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/logging"
)

// configFile is one generated artefact to be activated atomically.
type configFile struct {
	// Path is the absolute destination, already resolved through SafeJoin.
	Path string
	// Content is the rendered file body.
	Content []byte
	// Mode is the permission bits applied to the file.
	Mode os.FileMode
	// Remove deletes the destination instead of writing it.
	Remove bool
}

// snapshot remembers a file's previous state so a failed validation can be
// rolled back exactly, including the case where the file did not exist.
type snapshot struct {
	path    string
	content []byte
	mode    os.FileMode
	existed bool
}

// applyPlan describes a full activation: files to write, the service's own
// validation command, and the reload that follows a successful validation.
type applyPlan struct {
	// Files are written (or removed) atomically before validation runs.
	Files []configFile
	// Check is the service's config-check argv; empty skips validation.
	Check []string
	// CheckArgs are appended to Check.
	CheckArgs []string
	// Reload is the argv that makes the service adopt the new config.
	Reload []string
	// ReloadArgs are appended to Reload.
	ReloadArgs []string
	// Service names the subsystem for logs and for the API-visible message.
	Service string
}

// apply writes every file, validates with the service's own checker, and only
// then reloads. If validation or the reload fails, every file is restored to
// its previous content and a typed error is returned. The raw command output
// is logged server-side and never travels back to an API caller, because it
// contains host paths and configuration excerpts.
func (s *Service) apply(ctx context.Context, plan applyPlan) error {
	snaps := make([]snapshot, 0, len(plan.Files))
	for _, f := range plan.Files {
		snap, err := takeSnapshot(f.Path)
		if err != nil {
			return err
		}
		snaps = append(snaps, snap)
	}

	for _, f := range plan.Files {
		if f.Remove {
			if err := os.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				restoreAll(snaps)
				return internalErr(err, "the configuration could not be updated")
			}
			continue
		}
		if err := writeFileAtomic(f.Path, f.Content, f.Mode); err != nil {
			restoreAll(snaps)
			return err
		}
	}

	if len(plan.Check) > 0 {
		out, err := s.run(ctx, plan.Check, plan.CheckArgs...)
		if err != nil {
			restoreAll(snaps)
			s.reloadQuietly(ctx, plan)
			logging.L().Error("hosting configuration rejected by service check",
				"service", plan.Service, "output", string(out), "error", err.Error())
			return apperr.Wrap(err, apperr.CodeValidation, 422,
				"the generated "+plan.Service+" configuration was rejected and has been rolled back")
		}
	}

	if len(plan.Reload) > 0 {
		out, err := s.run(ctx, plan.Reload, plan.ReloadArgs...)
		if err != nil {
			restoreAll(snaps)
			s.reloadQuietly(ctx, plan)
			logging.L().Error("hosting service reload failed",
				"service", plan.Service, "output", string(out), "error", err.Error())
			return apperr.Wrap(err, apperr.CodeUnavailable, 503,
				"the "+plan.Service+" service could not be reloaded and the change has been rolled back")
		}
	}

	return nil
}

// reloadQuietly re-applies the restored configuration on a best-effort basis
// after a rollback so the service does not keep serving the rejected state.
func (s *Service) reloadQuietly(ctx context.Context, plan applyPlan) {
	if len(plan.Reload) == 0 {
		return
	}
	if _, err := s.run(ctx, plan.Reload, plan.ReloadArgs...); err != nil {
		logging.L().Error("hosting rollback reload failed", "service", plan.Service, "error", err.Error())
	}
}

// takeSnapshot records the current content and mode of path.
func takeSnapshot(path string) (snapshot, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return snapshot{path: path}, nil
		}
		return snapshot{}, internalErr(err, "the configuration could not be read")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return snapshot{}, internalErr(err, "the configuration could not be read")
	}
	return snapshot{path: path, content: content, mode: info.Mode().Perm(), existed: true}, nil
}

// readFileIfExists returns the content of path, or nil when it is absent. It
// lets a resync compare generated output against what is on disk.
func readFileIfExists(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, internalErr(err, "the configuration could not be read")
	}
	return content, nil
}

// restoreAll puts every snapshotted file back the way it was.
func restoreAll(snaps []snapshot) {
	for _, snap := range snaps {
		if !snap.existed {
			if err := os.Remove(snap.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				logging.L().Error("hosting rollback could not remove a generated file", "error", err.Error())
			}
			continue
		}
		if err := writeFileAtomic(snap.path, snap.content, snap.mode); err != nil {
			logging.L().Error("hosting rollback could not restore a generated file", "error", err.Error())
		}
	}
}

// writeFileAtomic writes content to a temporary file in the destination
// directory and renames it into place, so a reader never sees a partial file.
func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := ensureDir(dir, dirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cashp-*")
	if err != nil {
		return internalErr(err, "the configuration could not be written")
	}
	tmpName := tmp.Name()
	defer func() {
		if _, statErr := os.Stat(tmpName); statErr == nil {
			if rmErr := os.Remove(tmpName); rmErr != nil {
				logging.L().Error("hosting could not clean a temporary file", "error", rmErr.Error())
			}
		}
	}()

	if _, err = tmp.Write(content); err != nil {
		tmp.Close()
		return internalErr(err, "the configuration could not be written")
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return internalErr(err, "the configuration could not be written")
	}
	if err = tmp.Close(); err != nil {
		return internalErr(err, "the configuration could not be written")
	}
	if err = os.Chmod(tmpName, mode); err != nil {
		return internalErr(err, "the configuration could not be written")
	}
	if err = os.Rename(tmpName, path); err != nil {
		return internalErr(err, "the configuration could not be written")
	}
	return nil
}
