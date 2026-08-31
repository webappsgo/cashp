package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"
)

// setupTokenLifetime is how long the one-time restore setup token stays
// valid, per AI.md PART 22 "Security Considerations".
const setupTokenLifetime = 24 * time.Hour

// RestoreResult reports what a restore did. On a restore to a NEW server
// RequiresSetupToken is true and SetupToken carries the one-time token the
// Primary Admin must present at /server/{admin_path} before regaining
// admin access; the token is shown once and is never written to disk by
// this package. Additional local admins are unaffected and can log in
// immediately with their existing credentials.
type RestoreResult struct {
	Manifest           Manifest
	RestoredFiles      []string
	NewServer          bool
	RequiresSetupToken bool
	SetupToken         string
	SetupTokenExpires  time.Time
	VersionWarning     string
}

// Restore verifies a backup and, only if every check passes, writes its
// contents over the current configuration and databases. Verification
// covers the checksum, the manifest, the decrypt test, and version
// compatibility; any failure aborts before a single file is written.
//
// When newServer is true the restore does not grant admin access: a
// one-time setup token is generated and the Primary Admin must
// re-authenticate with it. Their username, password, 2FA, API token, and
// all configuration are preserved by the restore itself.
func (s *Service) Restore(ctx context.Context, path, password string, newServer bool) (RestoreResult, error) {
	m, files, err := s.verify(ctx, path, password)
	if err != nil {
		return RestoreResult{}, err
	}

	targets := make(map[string]string, len(files))

	for _, e := range m.Contents {
		target, err := s.destination(e.Path)
		if err != nil {
			return RestoreResult{}, err
		}
		targets[e.Path] = target
	}

	result := RestoreResult{
		Manifest:       *m,
		NewServer:      newServer,
		VersionWarning: m.VersionWarning,
	}

	for _, e := range m.Contents {
		if err := ctx.Err(); err != nil {
			return RestoreResult{}, err
		}

		mode := os.FileMode(e.Mode).Perm()
		if mode == 0 {
			mode = 0o600
		}

		if err := writeFileAtomic(targets[e.Path], files[e.Path], mode); err != nil {
			return RestoreResult{}, err
		}

		result.RestoredFiles = append(result.RestoredFiles, targets[e.Path])
	}

	if newServer {
		token, err := newSetupToken()
		if err != nil {
			return RestoreResult{}, err
		}

		result.RequiresSetupToken = true
		result.SetupToken = token
		result.SetupTokenExpires = s.now().UTC().Add(setupTokenLifetime)
	}

	return result, nil
}

// destination maps a manifest path onto its restore location: the config
// namespace lands in ConfigDir, the data namespace in DataDir. Anything
// else, or any path attempting to escape its root, is refused.
func (s *Service) destination(rel string) (string, error) {
	switch {
	case strings.HasPrefix(rel, configRoot+"/"):
		return safeJoin(s.opts.ConfigDir, strings.TrimPrefix(rel, configRoot+"/"))
	case strings.HasPrefix(rel, dataRoot+"/"):
		return safeJoin(s.opts.DataDir, strings.TrimPrefix(rel, dataRoot+"/"))
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsafePath, rel)
	}
}

// newSetupToken returns a 32-character one-time token for Primary Admin
// re-authentication after a restore to a new server.
func newSetupToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}

	return hex.EncodeToString(raw), nil
}
