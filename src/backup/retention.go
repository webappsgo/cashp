package backup

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// fullPattern matches a full backup: a scheduled daily
// ({app}_backup_YYYY-MM-DD) or a manual/timestamped one
// ({app}_backup_YYYY-MM-DD_HHMMSS), encrypted or not.
var fullPattern = regexp.MustCompile(`^` + regexp.QuoteMeta(AppName) + `_backup_(\d{4}-\d{2}-\d{2})(?:_(\d{6}))?\.tar\.gz(?:\.enc)?$`)

// incrementalPattern matches the daily and hourly incrementals, which are
// always exactly one file each and are exempt from count-based retention.
var incrementalPattern = regexp.MustCompile(`^` + regexp.QuoteMeta(AppName) + `-(daily|hourly)\.tar\.gz(?:\.enc)?$`)

// backupFile is one archive found in the backup directory.
type backupFile struct {
	path        string
	name        string
	when        time.Time
	size        int64
	incremental bool
}

// scan lists every archive in the cached backup directory that matches the
// naming this application creates. Any other file matching the app's
// prefixes is treated as a full backup, so nothing the app wrote is ever
// exempt from pruning. The backup directory is never re-resolved here.
func (s *Service) scan() ([]backupFile, error) {
	entries, err := os.ReadDir(s.opts.BackupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	var out []backupFile

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !isBackupName(name) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return nil, err
		}

		file := backupFile{
			path:        filepath.Join(s.opts.BackupDir, name),
			name:        name,
			size:        info.Size(),
			when:        info.ModTime().UTC(),
			incremental: incrementalPattern.MatchString(name),
		}

		if m := fullPattern.FindStringSubmatch(name); m != nil {
			layout, value := dateLayout, m[1]
			if m[2] != "" {
				layout, value = stampLayout, m[1]+"_"+m[2]
			}
			if t, err := time.Parse(layout, value); err == nil {
				file.when = t.UTC()
			}
		}

		out = append(out, file)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].when.Equal(out[j].when) {
			return out[i].name < out[j].name
		}

		return out[i].when.After(out[j].when)
	})

	return out, nil
}

// isBackupName reports whether a file name belongs to this application's
// backup namespace.
func isBackupName(name string) bool {
	if !strings.Contains(name, PlainExt) {
		return false
	}

	return strings.HasPrefix(name, AppName+"_backup_") || strings.HasPrefix(name, AppName+"-")
}

// Prune applies the grandfather-father-son retention policy to the backup
// directory and deletes everything it does not keep, returning the paths
// removed. Backups are examined newest first; each one fills the highest
// priority slot it qualifies for (yearly, then monthly, then weekly, then
// daily) and is deleted only when every slot it qualifies for is full.
// After count-based pruning, a non-zero MaxTotalSize deletes the oldest
// remaining full backups until the directory fits under the cap.
// Incrementals are replaced on each run and are never counted or deleted.
func (s *Service) Prune(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	files, err := s.scan()
	if err != nil {
		return nil, err
	}

	policy, _ := NormalizeRetention(s.opts.Retention)

	var (
		kept    []backupFile
		removed []string
		total   int64

		yearly, monthly, weekly, daily int
	)

	for _, f := range files {
		if f.incremental {
			total += f.size

			continue
		}

		switch {
		case isYearly(f.when) && yearly < policy.Yearly:
			yearly++
		case isMonthly(f.when) && monthly < policy.Monthly:
			monthly++
		case isWeekly(f.when) && weekly < policy.Weekly:
			weekly++
		case daily < policy.Daily:
			daily++
		default:
			if err := os.Remove(f.path); err != nil {
				return nil, err
			}
			removed = append(removed, f.path)

			continue
		}

		kept = append(kept, f)
		total += f.size
	}

	if policy.MaxTotalSize > 0 {
		for i := len(kept) - 1; i >= 0 && total > policy.MaxTotalSize; i-- {
			if err := os.Remove(kept[i].path); err != nil {
				return nil, err
			}
			removed = append(removed, kept[i].path)
			total -= kept[i].size
		}
	}

	sort.Strings(removed)

	return removed, nil
}

// isYearly reports whether t falls on January 1st.
func isYearly(t time.Time) bool {
	return t.Month() == time.January && t.Day() == 1
}

// isMonthly reports whether t falls on the 1st of a month.
func isMonthly(t time.Time) bool {
	return t.Day() == 1
}

// isWeekly reports whether t falls on a Sunday.
func isWeekly(t time.Time) bool {
	return t.Weekday() == time.Sunday
}

// List returns the manifest of every backup in the backup directory,
// newest first. An encrypted archive cannot be opened without its
// password, so its entry carries the metadata visible from disk with
// Readable false; the same is true of an archive that fails to parse.
func (s *Service) List() ([]Manifest, error) {
	files, err := s.scan()
	if err != nil {
		return nil, err
	}

	out := make([]Manifest, 0, len(files))

	for _, f := range files {
		stub := Manifest{
			CreatedAt: f.when,
			Path:      f.path,
			FileSize:  f.size,
		}

		raw, err := os.ReadFile(f.path)
		if err != nil {
			return nil, err
		}

		if IsEncrypted(raw) {
			stub.Encrypted = true
			stub.EncryptionMethod = EncryptionMethod
			stub.KeyDerivation = KeyDerivation
			out = append(out, stub)

			continue
		}

		m, _, err := readArchive(raw)
		if err != nil {
			out = append(out, stub)

			continue
		}

		m.Path = f.path
		m.FileSize = f.size
		m.Readable = true
		out = append(out, *m)
	}

	return out, nil
}
