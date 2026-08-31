package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/notify"
)

// fixture is a throwaway server tree: a config dir, a data dir, and a
// backup dir, all under t.TempDir().
type fixture struct {
	configDir string
	dataDir   string
	backupDir string
	service   *Service
}

// newFixture builds a minimal but valid server tree containing the three
// always-required files plus a custom template, and returns a Service
// bound to it.
func newFixture(t *testing.T, compliance bool) *fixture {
	t.Helper()

	root := t.TempDir()

	f := &fixture{
		configDir: filepath.Join(root, "config"),
		dataDir:   filepath.Join(root, "data"),
		backupDir: filepath.Join(root, "backup"),
	}

	mkdir(t, f.configDir)
	mkdir(t, f.dataDir)
	mkdir(t, filepath.Join(f.configDir, "template", "email"))

	writeFile(t, filepath.Join(f.configDir, "server.yml"), []byte("mode: production\nport: 8080\n"))
	writeFile(t, filepath.Join(f.configDir, "template", "email", "welcome.html"), []byte("<p>welcome</p>"))
	writeFile(t, filepath.Join(f.dataDir, "server.db"), fakeDatabase("server", 40*1024))
	writeFile(t, filepath.Join(f.dataDir, "users.db"), fakeDatabase("users", 40*1024))

	f.service = New(Options{
		ConfigDir:  f.configDir,
		DataDir:    f.dataDir,
		BackupDir:  f.backupDir,
		Compliance: compliance,
		AppVersion: "1.2.3",
		CreatedBy:  "administrator",
		Retention:  RetentionPolicy{Daily: 1},
		Now:        func() time.Time { return time.Date(2026, 2, 11, 2, 0, 0, 0, time.UTC) },
	})

	return f
}

// fakeDatabase returns bytes that start with a valid SQLite 3 header so the
// database integrity check passes, padded to size with repeating content.
func fakeDatabase(tag string, size int) []byte {
	out := make([]byte, 0, size)
	out = append(out, []byte(sqliteMagic)...)

	page := []byte("row:" + tag + ":0123456789abcdef0123456789abcdef")
	for len(out) < size {
		out = append(out, page...)
	}

	return out[:size]
}

func mkdir(t *testing.T, dir string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return data
}

func TestCreateVerifyRestoreRoundTrip(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()

	original := readFile(t, filepath.Join(f.dataDir, "users.db"))

	path, manifest, err := f.service.Create(ctx, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if filepath.Ext(path) != ".gz" {
		t.Fatalf("expected an unencrypted .tar.gz, got %s", path)
	}

	if manifest.Encrypted {
		t.Fatal("manifest reports encryption for an unencrypted backup")
	}

	for _, required := range requiredFiles {
		if _, ok := manifest.Entry(required); !ok {
			t.Fatalf("manifest is missing required file %s", required)
		}
	}

	if manifest.Checksum == "" || len(manifest.BlockIndex) == 0 {
		t.Fatal("manifest must record a checksum and a block index")
	}

	if _, err := f.service.Verify(ctx, path, ""); err != nil {
		t.Fatalf("verify: %v", err)
	}

	writeFile(t, filepath.Join(f.dataDir, "users.db"), fakeDatabase("clobbered", 1024))
	writeFile(t, filepath.Join(f.configDir, "server.yml"), []byte("mode: broken\n"))

	result, err := f.service.Restore(ctx, path, "", false)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	if result.RequiresSetupToken || result.SetupToken != "" {
		t.Fatal("restore to the same server must not issue a setup token")
	}

	if len(result.RestoredFiles) != len(manifest.Contents) {
		t.Fatalf("restored %d files, manifest holds %d", len(result.RestoredFiles), len(manifest.Contents))
	}

	if !bytes.Equal(readFile(t, filepath.Join(f.dataDir, "users.db")), original) {
		t.Fatal("users.db was not restored byte for byte")
	}

	if string(readFile(t, filepath.Join(f.configDir, "server.yml"))) != "mode: production\nport: 8080\n" {
		t.Fatal("server.yml was not restored")
	}

	if string(readFile(t, filepath.Join(f.configDir, "template", "email", "welcome.html"))) != "<p>welcome</p>" {
		t.Fatal("custom template was not restored")
	}
}

func TestCreateEncryptedRoundTrip(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()

	const password = "correct horse battery staple"

	path, manifest, err := f.service.Create(ctx, password)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if filepath.Ext(path) != ".enc" {
		t.Fatalf("expected an encrypted .tar.gz.enc, got %s", path)
	}

	if !manifest.Encrypted || manifest.EncryptionMethod != EncryptionMethod || manifest.KeyDerivation != KeyDerivation {
		t.Fatalf("manifest does not describe the encryption: %+v", manifest)
	}

	raw := readFile(t, path)
	if !IsEncrypted(raw) {
		t.Fatal("archive on disk is not encrypted")
	}

	if bytes.Contains(raw, []byte(password)) || bytes.Contains(raw, []byte("mode: production")) {
		t.Fatal("encrypted archive leaks the password or plaintext content")
	}

	if _, err := f.service.Verify(ctx, path, ""); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("expected ErrPasswordRequired, got %v", err)
	}

	if _, err := f.service.Verify(ctx, path, "wrong password"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("expected ErrInvalidPassword, got %v", err)
	}

	if _, err := f.service.Verify(ctx, path, password); err != nil {
		t.Fatalf("verify with the correct password: %v", err)
	}

	result, err := f.service.Restore(ctx, path, password, true)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	if !result.RequiresSetupToken {
		t.Fatal("restore to a new server must require setup-token re-authentication")
	}

	if len(result.SetupToken) != 32 {
		t.Fatalf("expected a 32-character setup token, got %q", result.SetupToken)
	}

	if !result.SetupTokenExpires.After(result.Manifest.CreatedAt) {
		t.Fatal("setup token must carry an expiry in the future")
	}
}

func TestComplianceRefusesUnencryptedBackup(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()

	if _, _, err := f.service.Create(ctx, ""); !errors.Is(err, ErrEncryptionRequired) {
		t.Fatalf("expected ErrEncryptionRequired, got %v", err)
	}

	entries, err := os.ReadDir(f.backupDir)
	if err == nil && len(entries) != 0 {
		t.Fatal("compliance mode must not leave any backup file behind")
	}

	path, manifest, err := f.service.Create(ctx, "compliance password")
	if err != nil {
		t.Fatalf("create with password: %v", err)
	}

	if !manifest.Encrypted {
		t.Fatal("compliance backup must be encrypted")
	}

	if !IsEncrypted(readFile(t, path)) {
		t.Fatal("compliance backup on disk must be encrypted")
	}
}

func TestTamperedBackupIsRefused(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()

	path, _, err := f.service.Create(ctx, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	manifest, blocks, err := readArchive(readFile(t, path))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	target := manifest.BlockIndex[0].Hash
	tampered := append([]byte(nil), blocks[target]...)
	tampered[0] ^= 0xFF
	blocks[target] = tampered

	body, err := buildArchive(manifest, blocks)
	if err != nil {
		t.Fatalf("rebuild archive: %v", err)
	}

	writeFile(t, path, body)

	if _, err := f.service.Verify(ctx, path, ""); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch, got %v", err)
	}

	sentinel := []byte("do not overwrite me\n")
	writeFile(t, filepath.Join(f.configDir, "server.yml"), sentinel)

	if _, err := f.service.Restore(ctx, path, "", false); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("restore must refuse a tampered backup, got %v", err)
	}

	if !bytes.Equal(readFile(t, filepath.Join(f.configDir, "server.yml")), sentinel) {
		t.Fatal("a refused restore must not write anything")
	}
}

func TestTruncatedAndEmptyBackupsAreRefused(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()

	empty := filepath.Join(f.backupDir, AppName+"_backup_2026-02-11_020000.tar.gz")
	mkdir(t, f.backupDir)
	writeFile(t, empty, nil)

	if _, err := f.service.Verify(ctx, empty, ""); !errors.Is(err, ErrEmptyBackup) {
		t.Fatalf("expected ErrEmptyBackup, got %v", err)
	}

	garbage := filepath.Join(f.backupDir, AppName+"_backup_2026-02-10_020000.tar.gz")
	writeFile(t, garbage, []byte("this is not a gzip stream"))

	if _, err := f.service.Verify(ctx, garbage, ""); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("expected ErrInvalidFormat, got %v", err)
	}
}

func TestCreateRequiresTheAlwaysIncludedFiles(t *testing.T) {
	f := newFixture(t, false)

	if err := os.Remove(filepath.Join(f.dataDir, "users.db")); err != nil {
		t.Fatalf("remove users.db: %v", err)
	}

	if _, _, err := f.service.Create(context.Background(), ""); !errors.Is(err, ErrMissingRequiredFile) {
		t.Fatalf("expected ErrMissingRequiredFile, got %v", err)
	}
}

func TestListReportsPlainAndEncryptedBackups(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()

	plain, _, err := f.service.Create(ctx, "")
	if err != nil {
		t.Fatalf("create plain: %v", err)
	}

	f.service.opts.Now = func() time.Time { return time.Date(2026, 2, 12, 2, 0, 0, 0, time.UTC) }

	encrypted, _, err := f.service.Create(ctx, "secret")
	if err != nil {
		t.Fatalf("create encrypted: %v", err)
	}

	list, err := f.service.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(list))
	}

	if list[0].Path != encrypted || !list[0].Encrypted || list[0].Readable {
		t.Fatalf("newest entry should be the unreadable encrypted archive: %+v", list[0])
	}

	if list[1].Path != plain || !list[1].Readable || list[1].AppVersion != "1.2.3" {
		t.Fatalf("oldest entry should be the readable plain archive: %+v", list[1])
	}
}

func TestVersionCompatibility(t *testing.T) {
	if warning, err := checkVersion(FormatVersion); err != nil || warning != "" {
		t.Fatalf("current version must verify cleanly: warning=%q err=%v", warning, err)
	}

	warning, err := checkVersion("0.9.0")
	if err != nil {
		t.Fatalf("older format must be restorable: %v", err)
	}
	if warning == "" {
		t.Fatal("older format must produce a version warning")
	}

	if _, err := checkVersion("2.0.0"); !errors.Is(err, ErrIncompatibleVersion) {
		t.Fatalf("expected ErrIncompatibleVersion, got %v", err)
	}

	if _, err := checkVersion("banana"); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("expected ErrInvalidFormat, got %v", err)
	}
}

func TestRestoreRejectsUnsafeManifestPaths(t *testing.T) {
	f := newFixture(t, false)

	for _, rel := range []string{"config/../../escape.yml", "etc/passwd", "/absolute"} {
		if _, err := f.service.destination(rel); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("path %q must be refused, got %v", rel, err)
		}
	}
}

// newTestNotifier builds a real *notify.Notifier backed by a throwaway
// SQLite database, so notifyCreate's dispatch can be observed through the
// store's dedup claims without a live SMTP server or webhook target.
func newTestNotifier(t *testing.T) *notify.Notifier {
	t.Helper()

	db, err := database.Open(database.Config{Driver: database.DriverSQLite, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	n, err := notify.New(notify.Options{DB: db, ConfigDir: t.TempDir(), AppName: "cashp"})
	if err != nil {
		t.Fatalf("new notifier: %v", err)
	}
	return n
}

func TestCreateWithoutNotifierSkipsNotification(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()

	if _, _, err := f.service.Create(ctx, ""); err != nil {
		t.Fatalf("create: %v", err)
	}
}

func TestCreateNotifiesBackupComplete(t *testing.T) {
	f := newFixture(t, false)
	f.service.opts.Notifier = newTestNotifier(t)
	ctx := context.Background()

	if _, _, err := f.service.Create(ctx, ""); err != nil {
		t.Fatalf("create: %v", err)
	}

	held, err := f.service.opts.Notifier.Store().DedupHeld(ctx, notify.EventBackupComplete+":")
	if err != nil {
		t.Fatalf("dedup held: %v", err)
	}
	if !held {
		t.Fatal("expected backup_complete to have been dispatched")
	}
}

func TestCreateNotifiesBackupFailed(t *testing.T) {
	f := newFixture(t, true)
	f.service.opts.Notifier = newTestNotifier(t)
	ctx := context.Background()

	if _, _, err := f.service.Create(ctx, ""); err == nil {
		t.Fatal("expected compliance mode to refuse an unencrypted backup")
	}

	held, err := f.service.opts.Notifier.Store().DedupHeld(ctx, notify.EventBackupFailed+":")
	if err != nil {
		t.Fatalf("dedup held: %v", err)
	}
	if !held {
		t.Fatal("expected backup_failed to have been dispatched")
	}
}
