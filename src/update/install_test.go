package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webappsgo/cashp/src/notify"
)

// oldContent is what the installed binary holds before an update.
const oldContent = "OLD BINARY"

// newContent is the payload the fake release serves.
const newContent = "NEW BINARY PAYLOAD"

// assetServer serves the release payload and a sha256.txt whose digest is
// whatever declaredSum says, so a mismatch can be simulated.
func assetServer(t *testing.T, assetName, payload, declaredSum string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(payload)); err != nil {
			t.Errorf("write payload: %v", err)
		}
	})
	mux.HandleFunc("/sha256.txt", func(w http.ResponseWriter, r *http.Request) {
		body := fmt.Sprintf("%s  %s\n%s  some-other-file\n", declaredSum, assetName, strings.Repeat("0", 64))
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("write checksum: %v", err)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

// installFixture prepares an installed binary in a temp dir and returns its
// path plus the asset name the updater will look for.
func installFixture(t *testing.T, name string) (string, string) {
	t.Helper()

	dir := t.TempDir()
	target := filepath.Join(dir, name)
	if err := os.WriteFile(target, []byte(oldContent), 0o755); err != nil {
		t.Fatalf("write fixture binary: %v", err)
	}

	return target, assetNameFor(target)
}

// sum returns the hex SHA256 of s.
func sum(s string) string {
	h := sha256.Sum256([]byte(s))

	return hex.EncodeToString(h[:])
}

// releaseFor builds a Release whose assets point at srv.
func releaseFor(srv *httptest.Server, assetName string, withChecksum bool) Release {
	assets := []Asset{{Name: assetName, URL: srv.URL + "/download"}}
	if withChecksum {
		assets = append(assets, Asset{Name: checksumAsset, URL: srv.URL + "/sha256.txt"})
	}

	return Release{Version: "v1.3.0", Channel: ChannelStable, Assets: assets}
}

// readFile reads a file or fails the test.
func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(data)
}

func TestInstallReplacesBinaryAndRetainsBackup(t *testing.T) {
	target, assetName := installFixture(t, "cashp")
	srv := assetServer(t, assetName, newContent, sum(newContent))

	s := newTestService(srv, Options{Channel: ChannelStable, BinaryPath: target})
	if err := s.Install(context.Background(), releaseFor(srv, assetName, true)); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if got := readFile(t, target); got != newContent {
		t.Errorf("installed binary = %q, want %q", got, newContent)
	}
	if got := readFile(t, target+backupSuffix); got != oldContent {
		t.Errorf("retained backup = %q, want %q", got, oldContent)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Errorf("installed mode = %o, want 0755", info.Mode().Perm())
	}

	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("directory holds %d entries, want the binary and its backup only", len(entries))
	}
}

func TestInstallWithoutNotifierSkipsNotification(t *testing.T) {
	target, assetName := installFixture(t, "cashp")
	srv := assetServer(t, assetName, newContent, sum(newContent))

	s := newTestService(srv, Options{Channel: ChannelStable, BinaryPath: target})
	if err := s.Install(context.Background(), releaseFor(srv, assetName, true)); err != nil {
		t.Fatalf("Install: %v", err)
	}
}

func TestInstallNotifiesUpdateInstalled(t *testing.T) {
	target, assetName := installFixture(t, "cashp")
	srv := assetServer(t, assetName, newContent, sum(newContent))

	s := newTestService(srv, Options{Channel: ChannelStable, BinaryPath: target, CurrentVersion: "v1.2.0"})
	s.opts.Notifier = newTestNotifier(t)
	ctx := context.Background()

	if err := s.Install(ctx, releaseFor(srv, assetName, true)); err != nil {
		t.Fatalf("Install: %v", err)
	}

	held, err := s.opts.Notifier.Store().DedupHeld(ctx, notify.EventUpdateInstalled+":")
	if err != nil {
		t.Fatalf("dedup held: %v", err)
	}
	if !held {
		t.Fatal("expected update_installed to have been dispatched")
	}
}

func TestInstallRefusesChecksumMismatch(t *testing.T) {
	target, assetName := installFixture(t, "cashp-cli")
	srv := assetServer(t, assetName, newContent, sum("something else"))

	s := newTestService(srv, Options{Channel: ChannelStable, BinaryPath: target})
	err := s.Install(context.Background(), releaseFor(srv, assetName, true))
	if err == nil {
		t.Fatal("a checksum mismatch must refuse the update")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want a checksum mismatch", err)
	}

	if got := readFile(t, target); got != oldContent {
		t.Errorf("installed binary = %q, want it untouched", got)
	}
	if _, err := os.Stat(target + backupSuffix); !os.IsNotExist(err) {
		t.Error("a refused update must not create a backup")
	}

	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want no leftover temp file", len(entries))
	}
}

func TestInstallRefusesReleaseWithoutChecksumAsset(t *testing.T) {
	target, assetName := installFixture(t, "cashp-agent")
	srv := assetServer(t, assetName, newContent, sum(newContent))

	s := newTestService(srv, Options{Channel: ChannelStable, BinaryPath: target})
	err := s.Install(context.Background(), releaseFor(srv, assetName, false))
	if err == nil {
		t.Fatal("a release without sha256.txt must be refused")
	}
	if !strings.Contains(err.Error(), checksumAsset) {
		t.Errorf("error = %v, want it to name the missing %s asset", err, checksumAsset)
	}
	if got := readFile(t, target); got != oldContent {
		t.Errorf("installed binary = %q, want it untouched", got)
	}
}

func TestInstallRefusesReleaseWithoutPlatformAsset(t *testing.T) {
	target, assetName := installFixture(t, "cashp")
	srv := assetServer(t, assetName, newContent, sum(newContent))

	rel := releaseFor(srv, "cashp-someos-somearch", true)
	s := newTestService(srv, Options{Channel: ChannelStable, BinaryPath: target})
	if err := s.Install(context.Background(), rel); err == nil {
		t.Fatal("a release without this platform's asset must be refused")
	}
	if got := readFile(t, target); got != oldContent {
		t.Errorf("installed binary = %q, want it untouched", got)
	}
}

func TestRollbackRestoresPreviousBinary(t *testing.T) {
	target, assetName := installFixture(t, "cashp")
	srv := assetServer(t, assetName, newContent, sum(newContent))

	s := newTestService(srv, Options{Channel: ChannelStable, BinaryPath: target})
	if err := s.Install(context.Background(), releaseFor(srv, assetName, true)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := s.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	if got := readFile(t, target); got != oldContent {
		t.Errorf("rolled back binary = %q, want %q", got, oldContent)
	}
	if _, err := os.Stat(target + backupSuffix); !os.IsNotExist(err) {
		t.Error("rollback must consume the retained backup")
	}
}

func TestRollbackWithoutBackup(t *testing.T) {
	target, _ := installFixture(t, "cashp")

	s := New(Options{BinaryPath: target})
	if err := s.Rollback(context.Background()); !errors.Is(err, ErrNoBackup) {
		t.Fatalf("Rollback = %v, want ErrNoBackup", err)
	}
}

func TestFetchChecksumMissingEntry(t *testing.T) {
	target, assetName := installFixture(t, "cashp")
	srv := assetServer(t, "unrelated-asset", newContent, sum(newContent))

	s := newTestService(srv, Options{BinaryPath: target})
	if _, err := s.fetchChecksum(context.Background(), srv.URL+"/sha256.txt", assetName); err == nil {
		t.Fatal("a sha256.txt without an entry for this asset must be an error")
	}
}

func TestInstallCancelledContext(t *testing.T) {
	target, assetName := installFixture(t, "cashp")
	srv := assetServer(t, assetName, newContent, sum(newContent))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := newTestService(srv, Options{BinaryPath: target})
	if err := s.Install(ctx, releaseFor(srv, assetName, true)); err == nil {
		t.Fatal("a cancelled context must abort the install")
	}
	if got := readFile(t, target); got != oldContent {
		t.Errorf("installed binary = %q, want it untouched", got)
	}
}
