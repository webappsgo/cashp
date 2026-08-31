package geoip

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateRequiresEnabled(t *testing.T) {
	d := newTestDB(t, Options{Enabled: false})

	if err := d.Update(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Update on a disabled DB = %v, want ErrDisabled", err)
	}

	var nilDB *DB
	if err := nilDB.Update(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Update on a nil DB = %v, want ErrDisabled", err)
	}
}

func TestUpdateRequiresDataDir(t *testing.T) {
	d, err := New(Options{Enabled: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	defer func() {
		if err := d.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	if err := d.Update(context.Background()); !errors.Is(err, ErrNoDataDir) {
		t.Fatalf("Update without a data directory = %v, want ErrNoDataDir", err)
	}
}

func TestUpdateStopsOnCancelledContextWithoutNetwork(t *testing.T) {
	d := newTestDB(t, Options{Enabled: true})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := d.Update(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Update with a cancelled context = %v, want context.Canceled", err)
	}

	entries, readErr := os.ReadDir(d.Dir())
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}

	if len(entries) != 0 {
		t.Fatalf("cancelled Update wrote %d files", len(entries))
	}
}

func TestVerifyDatabaseRejectsNonDatabases(t *testing.T) {
	dir := t.TempDir()

	path := filepath.Join(dir, "not-a-database.mmdb")
	if err := os.WriteFile(path, []byte("<html>404 Not Found</html>"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := verifyDatabase(path); err == nil {
		t.Fatal("verifyDatabase accepted a non-database file")
	}

	if err := verifyDatabase(filepath.Join(dir, "missing.mmdb")); err == nil {
		t.Fatal("verifyDatabase accepted a missing file")
	}
}

func TestOpenReaderIgnoresUnreadableFiles(t *testing.T) {
	d := newTestDB(t, Options{Enabled: true})

	path := filepath.Join(d.Dir(), fileCountry)
	if err := os.WriteFile(path, []byte("garbage"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	d.openReader(fileCountry)

	if d.reader(fileCountry) != nil {
		t.Fatal("openReader kept a reader for an unreadable file")
	}
}

func TestDatabaseURLs(t *testing.T) {
	urls := DatabaseURLs()

	want := map[string]string{
		fileASN:     "https://cdn.jsdelivr.net/npm/@ip-location-db/asn-mmdb/asn.mmdb",
		fileCountry: "https://cdn.jsdelivr.net/npm/@ip-location-db/geo-whois-asn-country-mmdb/geo-whois-asn-country.mmdb",
		fileCity4:   "https://cdn.jsdelivr.net/npm/@ip-location-db/dbip-city-mmdb/dbip-city-ipv4.mmdb",
		fileCity6:   "https://cdn.jsdelivr.net/npm/@ip-location-db/dbip-city-mmdb/dbip-city-ipv6.mmdb",
	}

	if len(urls) != len(want) {
		t.Fatalf("DatabaseURLs returned %d entries, want %d", len(urls), len(want))
	}

	for file, url := range want {
		if urls[file] != url {
			t.Fatalf("DatabaseURLs()[%q] = %q, want %q", file, urls[file], url)
		}
	}

	for _, url := range urls {
		if !strings.HasPrefix(url, "https://cdn.jsdelivr.net/npm/@ip-location-db/") {
			t.Fatalf("database URL %q is not an ip-location-db jsDelivr URL", url)
		}
	}
}
