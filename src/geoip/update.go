package geoip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/oschwald/maxminddb-golang"
)

// ErrNoDataDir is returned by Update when no data directory was
// configured, so there is nowhere to store the downloaded databases.
var ErrNoDataDir = errors.New("geoip: no data directory configured")

// ErrDisabled is returned by Update when GeoIP is turned off. Nothing is
// downloaded for a disabled subsystem.
var ErrDisabled = errors.New("geoip: disabled")

// maxDatabaseBytes caps a single download at 512 MiB, well above the
// largest ip-location-db file, so a corrupted or hostile response cannot
// exhaust the disk.
const maxDatabaseBytes = 512 << 20

// Update downloads every ip-location-db database from the jsDelivr CDN
// and swaps the new files in atomically. It is the body of the
// scheduler's geoip_update task and is also safe to call directly.
//
// Each file is written to a temporary path, verified to be a readable
// MMDB, then renamed over the previous copy. A database that fails to
// download or verify leaves the existing copy untouched; Update reports
// the joined errors so the scheduler can log them, and lookups keep
// working from whatever is already on disk.
func (d *DB) Update(ctx context.Context) error {
	if d == nil || !d.enabled {
		return ErrDisabled
	}

	if d.dir == "" {
		return ErrNoDataDir
	}

	if err := os.MkdirAll(d.dir, 0o750); err != nil {
		return err
	}

	var errs []error

	for _, src := range sources {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)

			break
		}

		if err := d.updateOne(ctx, src); err != nil {
			errs = append(errs, fmt.Errorf("geoip: %s: %w", src.file, err))

			continue
		}

		d.openReader(src.file)
	}

	return errors.Join(errs...)
}

// updateOne downloads a single database into d.dir, verifies it, and
// renames it over any previous copy.
func (d *DB) updateOne(ctx context.Context, src source) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "application/octet-stream")

	client := d.client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDatabaseBytes))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}

	final := filepath.Join(d.dir, src.file)

	tmp, err := os.CreateTemp(d.dir, src.file+".*.tmp")
	if err != nil {
		return err
	}

	tmpName := tmp.Name()

	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	written, err := io.Copy(tmp, io.LimitReader(resp.Body, maxDatabaseBytes))
	if err != nil {
		return err
	}

	if written == 0 {
		return errors.New("empty response body")
	}

	if written >= maxDatabaseBytes {
		return fmt.Errorf("database exceeds %d bytes", int64(maxDatabaseBytes))
	}

	if err := tmp.Sync(); err != nil {
		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	if err := verifyDatabase(tmpName); err != nil {
		return err
	}

	if err := os.Chmod(tmpName, 0o640); err != nil {
		return err
	}

	return os.Rename(tmpName, final)
}

// verifyDatabase confirms a freshly downloaded file parses as an MMDB
// before it replaces a working database. Opening the file validates the
// metadata section, which lives at the end of an MMDB and therefore
// rejects truncated downloads and CDN error pages.
func verifyDatabase(path string) error {
	r, err := maxminddb.Open(path)
	if err != nil {
		return err
	}

	defer func() {
		_ = r.Close()
	}()

	if r.Metadata.NodeCount == 0 {
		return errors.New("database contains no records")
	}

	return nil
}

// DatabaseURLs returns the jsDelivr CDN URL for every database, keyed by
// the local file name it is saved as.
func DatabaseURLs() map[string]string {
	out := make(map[string]string, len(sources))
	for _, src := range sources {
		out[src.file] = src.url
	}

	return out
}
