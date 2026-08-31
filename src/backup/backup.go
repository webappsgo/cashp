// Package backup implements cashp's backup and restore engine per AI.md
// PART 22. Archives are deduplicated (content-defined chunking with a
// block index in manifest.json), compressed (gzip), and optionally
// encrypted with AES-256-GCM under an Argon2id-derived key. The backup
// password is NEVER stored: it is supplied per operation. Encryption is
// optional unless compliance mode is enabled, in which case an
// unencrypted backup is refused. Nothing is ever restored unless every
// verification check passes.
package backup

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/logging"
	"github.com/webappsgo/cashp/src/notify"
	"github.com/webappsgo/cashp/src/scheduler"
)

// Archive layout and identity constants.
const (
	// AppName is the file-name prefix of every archive this package creates.
	AppName = "cashp"
	// FormatVersion is the manifest format version written by this build.
	FormatVersion = "1.0.0"
	// EncryptionMethod is the only supported archive cipher.
	EncryptionMethod = "AES-256-GCM"
	// KeyDerivation is the only supported password-to-key function.
	KeyDerivation = "argon2id"
	// ManifestName is the manifest entry name inside the tar stream.
	ManifestName = "manifest.json"
	// PlainExt is the extension of an unencrypted archive.
	PlainExt = ".tar.gz"
	// EncryptedExt is the extension of an encrypted archive.
	EncryptedExt = ".tar.gz.enc"

	// blockPrefix is the tar directory holding deduplicated content blocks.
	blockPrefix = "blocks/"
	// configRoot is the manifest path prefix for files taken from ConfigDir.
	configRoot = "config"
	// dataRoot is the manifest path prefix for files taken from DataDir.
	dataRoot = "data"
	// stampLayout is the timestamp used by manual/timestamped backups.
	stampLayout = "2006-01-02_150405"
	// dateLayout is the date used by scheduled daily full backups.
	dateLayout = "2006-01-02"
	// sqliteMagic is the 16-byte header every SQLite 3 database starts with.
	sqliteMagic = "SQLite format 3\x00"
)

// Sentinel errors. Callers may match these with errors.Is to decide how to
// report a failure; none of them are safe to echo verbatim to an end user
// without context.
var (
	// ErrEncryptionRequired is returned when compliance mode is enabled but no backup password was supplied.
	ErrEncryptionRequired = errors.New("backup: compliance mode requires an encrypted backup; set a backup password")
	// ErrPasswordRequired is returned when an encrypted archive is opened without a password.
	ErrPasswordRequired = errors.New("backup: encrypted backup requires password")
	// ErrInvalidPassword is returned when the supplied password does not decrypt the archive.
	ErrInvalidPassword = errors.New("backup: invalid backup password")
	// ErrInvalidFormat is returned when a file is not a readable cashp archive.
	ErrInvalidFormat = errors.New("backup: invalid backup format")
	// ErrManifestMissing is returned when an archive carries no readable manifest.json.
	ErrManifestMissing = errors.New("backup: manifest.json missing or unreadable")
	// ErrChecksumMismatch is returned when archive content does not match the manifest checksum.
	ErrChecksumMismatch = errors.New("backup: checksum mismatch, backup is corrupted")
	// ErrIncompatibleVersion is returned when the archive format is newer than this build can restore.
	ErrIncompatibleVersion = errors.New("backup: backup format is newer than this version can restore")
	// ErrMissingRequiredFile is returned when server.yml, server.db, or users.db is absent.
	ErrMissingRequiredFile = errors.New("backup: required file missing")
	// ErrDatabaseIntegrity is returned when a bundled database fails its integrity check.
	ErrDatabaseIntegrity = errors.New("backup: database integrity check failed")
	// ErrEmptyBackup is returned when the backup file exists but has zero length.
	ErrEmptyBackup = errors.New("backup: backup file is empty")
	// ErrUnsafePath is returned when an archive entry escapes its restore root.
	ErrUnsafePath = errors.New("backup: unsafe path in manifest")
)

// requiredFiles are the manifest paths that must exist in every archive per
// AI.md PART 22 "Backup Contents".
var requiredFiles = []string{
	configRoot + "/server.yml",
	dataRoot + "/server.db",
	dataRoot + "/users.db",
}

// Options configures a backup Service. ConfigDir, DataDir, and BackupDir
// are absolute paths; BackupDir is the directory resolved and cached at
// startup and is never re-resolved during a sweep.
type Options struct {
	ConfigDir  string
	DataDir    string
	BackupDir  string
	Compliance bool
	Retention  RetentionPolicy
	// IncludeSSL adds {config_dir}/ssl to the archive (--include-ssl).
	IncludeSSL bool
	// IncludeData adds the whole {data_dir} tree to the archive (--include-data).
	IncludeData bool
	// AppVersion is recorded in the manifest for version-compatibility reporting.
	AppVersion string
	// CreatedBy is the admin username recorded in the manifest.
	CreatedBy string
	// Now overrides the clock; nil means time.Now. Used by tests and by the scheduler.
	Now func() time.Time
	// Notifier delivers backup_complete/backup_failed notifications per
	// AI.md PART 18's decision matrix; nil disables notification entirely.
	Notifier *notify.Notifier
}

// RetentionPolicy is the grandfather-father-son retention configuration.
// Daily maps to max_backups, Weekly to keep_weekly, Monthly to
// keep_monthly, Yearly to keep_yearly. MaxTotalSize is a hard size cap in
// bytes that overrides every count limit; 0 disables it.
type RetentionPolicy struct {
	Daily        int
	Weekly       int
	Monthly      int
	Yearly       int
	MaxTotalSize int64
}

// NormalizeRetention applies the AI.md PART 22 validation table: invalid
// values fall back to their defaults and high values are accepted with a
// warning. It never fails, because an invalid retention setting must not
// stop the server from starting.
func NormalizeRetention(p RetentionPolicy) (RetentionPolicy, []string) {
	var warnings []string

	if p.Daily < 1 {
		warnings = append(warnings, "max_backups: "+strconv.Itoa(p.Daily)+" invalid, using default 1")
		p.Daily = 1
	}
	if p.Weekly < 0 {
		warnings = append(warnings, "keep_weekly: "+strconv.Itoa(p.Weekly)+" invalid, using default 0")
		p.Weekly = 0
	}
	if p.Monthly < 0 {
		warnings = append(warnings, "keep_monthly: "+strconv.Itoa(p.Monthly)+" invalid, using default 0")
		p.Monthly = 0
	}
	if p.Yearly < 0 {
		warnings = append(warnings, "keep_yearly: "+strconv.Itoa(p.Yearly)+" invalid, using default 0")
		p.Yearly = 0
	}
	if p.MaxTotalSize < 0 {
		warnings = append(warnings, "max_total_size: negative value invalid, disabling size cap")
		p.MaxTotalSize = 0
	}

	warnings = append(warnings, thresholdWarnings(p)...)

	return p, warnings
}

// thresholdWarnings reports retention counts that exceed the recommended
// ceilings in AI.md PART 22 "Warning Thresholds"; the values are accepted.
func thresholdWarnings(p RetentionPolicy) []string {
	var warnings []string

	if p.Daily > 7 {
		warnings = append(warnings, fmt.Sprintf("max_backups: %d exceeds recommended 7 (%d days of daily backups)", p.Daily, p.Daily))
	}
	if p.Weekly > 8 {
		warnings = append(warnings, fmt.Sprintf("keep_weekly: %d exceeds recommended 8 (more than 2 months of weekly backups)", p.Weekly))
	}
	if p.Monthly > 12 {
		warnings = append(warnings, fmt.Sprintf("keep_monthly: %d exceeds recommended 12 (more than a year of monthly backups)", p.Monthly))
	}
	if p.Yearly > 2 {
		warnings = append(warnings, fmt.Sprintf("keep_yearly: %d exceeds recommended 2 (more than 2 years of yearly backups)", p.Yearly))
	}

	return warnings
}

// Service creates, verifies, restores, lists, and prunes backups.
type Service struct {
	opts Options
}

// New returns a Service bound to opts.
func New(opts Options) *Service {
	return &Service{opts: opts}
}

// Options returns a copy of the configuration this Service was built with.
func (s *Service) Options() Options {
	return s.opts
}

// now returns the current time through the injectable clock.
func (s *Service) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}

	return time.Now()
}

// notifyCreate dispatches backup_complete on success or backup_failed on
// error, per AI.md PART 18's decision matrix: complete is WebUI-only,
// failed also emails since it is critical and needs attention. Both are
// tolerated best-effort - a notification failure never fails the backup
// that already succeeded or already failed on its own terms.
func (s *Service) notifyCreate(ctx context.Context, path string, m Manifest, createErr error) {
	if s.opts.Notifier == nil {
		return
	}

	var msg notify.Message
	if createErr != nil {
		msg = notify.Message{
			Event:       notify.EventBackupFailed,
			ExecutionID: scheduler.ExecutionIDFromContext(ctx),
			Vars:        map[string]string{"error": createErr.Error()},
		}
	} else {
		msg = notify.Message{
			Event: notify.EventBackupComplete,
			Vars: map[string]string{
				"filename": filepath.Base(path),
				"size":     strconv.FormatInt(m.StoredSize, 10),
			},
		}
	}

	if err := s.opts.Notifier.Notify(ctx, msg); err != nil {
		logging.L().Warn("backup notification failed",
			"event", msg.Event, "error", err.Error())
	}
}

// archiveName builds the file name for a manual/timestamped backup at t.
func archiveName(t time.Time, encrypted bool) string {
	ext := PlainExt
	if encrypted {
		ext = EncryptedExt
	}

	return AppName + "_backup_" + t.Format(stampLayout) + ext
}

// checkVersion compares an archive's manifest format version against this
// build. A newer major version is fatal; anything older produces a warning
// so the caller can report that schema updates may be applied.
func checkVersion(version string) (string, error) {
	if version == "" {
		return "", fmt.Errorf("%w: manifest has no version", ErrManifestMissing)
	}

	got, err := parseVersion(version)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidFormat, err)
	}

	want, err := parseVersion(FormatVersion)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidFormat, err)
	}

	if got[0] > want[0] {
		return "", fmt.Errorf("%w: backup format %s, this build supports %s", ErrIncompatibleVersion, version, FormatVersion)
	}

	if got[0] < want[0] || got[1] < want[1] {
		return fmt.Sprintf("backup format %s is older than %s; schema updates will be applied if needed", version, FormatVersion), nil
	}

	return "", nil
}

// parseVersion splits a dotted major.minor.patch string into its numbers.
func parseVersion(v string) ([3]int, error) {
	var out [3]int

	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("malformed version %q", v)
	}

	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("malformed version %q", v)
		}
		out[i] = n
	}

	return out, nil
}
