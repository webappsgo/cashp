package backup

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// seedBackups writes placeholder archives of the given size into dir and
// returns a Service configured with policy.
func seedBackups(t *testing.T, policy RetentionPolicy, size int, names ...string) (*Service, string) {
	t.Helper()

	dir := t.TempDir()

	for _, name := range names {
		writeFile(t, filepath.Join(dir, name), bytes.Repeat([]byte("x"), size))
	}

	return New(Options{BackupDir: dir, Retention: policy}), dir
}

// fullName builds a full-backup file name for a date or date_time stamp.
func fullName(stamp, ext string) string {
	return AppName + "_backup_" + stamp + ext
}

// incrementalName builds a daily or hourly incremental file name.
func incrementalName(kind, ext string) string {
	return AppName + "-" + kind + ext
}

// remaining lists the file names left in dir, sorted.
func remaining(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)

	return out
}

func TestPruneAppliesGrandfatherFatherSon(t *testing.T) {
	policy := RetentionPolicy{Daily: 2, Weekly: 2, Monthly: 2, Yearly: 1}

	names := []string{
		fullName("2026-02-11", PlainExt),
		fullName("2026-02-10", PlainExt),
		fullName("2026-01-01", PlainExt),
		fullName("2025-12-21", PlainExt),
		fullName("2025-12-14", PlainExt),
		fullName("2025-12-07", PlainExt),
		fullName("2025-12-01", PlainExt),
		fullName("2025-11-01", PlainExt),
		fullName("2025-10-01", PlainExt),
		incrementalName("daily", PlainExt),
		incrementalName("hourly", PlainExt),
	}

	service, dir := seedBackups(t, policy, 16, names...)

	removed, err := service.Prune(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	if len(removed) != 2 {
		t.Fatalf("expected 2 deletions, got %v", removed)
	}

	want := []string{
		incrementalName("daily", PlainExt),
		incrementalName("hourly", PlainExt),
		fullName("2025-11-01", PlainExt),
		fullName("2025-12-01", PlainExt),
		fullName("2025-12-14", PlainExt),
		fullName("2025-12-21", PlainExt),
		fullName("2026-01-01", PlainExt),
		fullName("2026-02-10", PlainExt),
		fullName("2026-02-11", PlainExt),
	}

	got := remaining(t, dir)
	if len(got) != len(want) {
		t.Fatalf("kept %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kept %v, want %v", got, want)
		}
	}
}

func TestPruneKeepsIncrementalsAndTimestampedBackups(t *testing.T) {
	policy := RetentionPolicy{Daily: 1}

	names := []string{
		fullName("2026-02-11_021500", EncryptedExt),
		fullName("2026-02-10_021500", EncryptedExt),
		incrementalName("daily", EncryptedExt),
	}

	service, dir := seedBackups(t, policy, 8, names...)

	removed, err := service.Prune(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	if len(removed) != 1 || filepath.Base(removed[0]) != fullName("2026-02-10_021500", EncryptedExt) {
		t.Fatalf("timestamped backups must count as dailies, oldest first: %v", removed)
	}

	got := remaining(t, dir)
	if len(got) != 2 {
		t.Fatalf("expected the newest full plus the incremental, got %v", got)
	}
}

func TestPruneSizeCapOverridesCountLimits(t *testing.T) {
	policy := RetentionPolicy{Daily: 5, MaxTotalSize: 250}

	names := []string{
		fullName("2026-02-11", PlainExt),
		fullName("2026-02-10", PlainExt),
		fullName("2026-02-09", PlainExt),
		fullName("2026-02-08", PlainExt),
	}

	service, dir := seedBackups(t, policy, 100, names...)

	removed, err := service.Prune(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	if len(removed) != 2 {
		t.Fatalf("size cap should have deleted 2 files, got %v", removed)
	}

	got := remaining(t, dir)
	want := []string{fullName("2026-02-10", PlainExt), fullName("2026-02-11", PlainExt)}

	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("kept %v, want %v", got, want)
	}
}

func TestPruneIgnoresForeignFiles(t *testing.T) {
	policy := RetentionPolicy{Daily: 1}

	service, dir := seedBackups(t, policy, 8,
		fullName("2026-02-11", PlainExt),
		fullName("2026-02-10", PlainExt),
	)

	writeFile(t, filepath.Join(dir, "notes.txt"), []byte("keep me"))
	writeFile(t, filepath.Join(dir, "other-app_backup_2026-02-01.tar.gz"), []byte("keep me too"))

	if _, err := service.Prune(context.Background()); err != nil {
		t.Fatalf("prune: %v", err)
	}

	for _, name := range []string{"notes.txt", "other-app_backup_2026-02-01.tar.gz"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("prune deleted a file it does not own: %s", name)
		}
	}
}

func TestNormalizeRetention(t *testing.T) {
	policy, warnings := NormalizeRetention(RetentionPolicy{Daily: 0, Weekly: -3, Monthly: -1, Yearly: -1, MaxTotalSize: -5})

	if policy.Daily != 1 || policy.Weekly != 0 || policy.Monthly != 0 || policy.Yearly != 0 || policy.MaxTotalSize != 0 {
		t.Fatalf("invalid values must fall back to defaults: %+v", policy)
	}

	if len(warnings) != 5 {
		t.Fatalf("expected a warning per invalid value, got %v", warnings)
	}

	_, warnings = NormalizeRetention(RetentionPolicy{Daily: 30, Weekly: 20, Monthly: 24, Yearly: 5})
	if len(warnings) != 4 {
		t.Fatalf("expected a threshold warning per high value, got %v", warnings)
	}
}

// pseudoRandom returns n deterministic pseudo-random bytes, standing in
// for the incompressible, non-repeating content of a real database file.
func pseudoRandom(n int, seed int64) []byte {
	out := make([]byte, n)
	rng := rand.New(rand.NewSource(seed))
	rng.Read(out)

	return out
}

func TestSplitBlocksDeduplicatesRepeatedContent(t *testing.T) {
	unit := pseudoRandom(256*1024, 20260211)
	data := append(append([]byte(nil), unit...), unit...)

	blocks := splitBlocks(data)
	if len(blocks) < 2 {
		t.Fatalf("expected several chunks, got %d", len(blocks))
	}

	unique := make(map[string][]byte)
	var total int

	for _, b := range blocks {
		total += len(b)
		unique[hashBytes(b)] = b
	}

	if total != len(data) {
		t.Fatalf("chunks cover %d bytes, input is %d", total, len(data))
	}

	var stored int
	for _, b := range unique {
		stored += len(b)
	}

	if stored >= total {
		t.Fatalf("deduplication stored %d of %d bytes; repeated content was not shared", stored, total)
	}
}

func TestSplitBlocksIsStableUnderInsertion(t *testing.T) {
	base := pseudoRandom(512*1024, 20260212)
	shifted := append([]byte("prefix"), base...)

	first := make(map[string]bool)
	for _, b := range splitBlocks(base) {
		first[hashBytes(b)] = true
	}

	var shared int
	blocks := splitBlocks(shifted)

	for _, b := range blocks {
		if first[hashBytes(b)] {
			shared++
		}
	}

	if shared*2 < len(blocks) {
		t.Fatalf("content-defined chunking shared only %d of %d chunks after an insertion", shared, len(blocks))
	}
}

func TestSplitBlocksIsDeterministic(t *testing.T) {
	data := fakeDatabase("determinism", 200*1024)

	first := splitBlocks(data)
	second := splitBlocks(data)

	if len(first) != len(second) {
		t.Fatalf("chunking is not deterministic: %d vs %d chunks", len(first), len(second))
	}

	for i := range first {
		if !bytes.Equal(first[i], second[i]) {
			t.Fatalf("chunk %d differs between runs", i)
		}
	}
}

func TestDedupSharesBlocksAcrossFiles(t *testing.T) {
	blocks := make(map[string][]byte)
	dir := t.TempDir()

	payload := pseudoRandom(128*1024, 20260213)
	writeFile(t, filepath.Join(dir, "a.db"), payload)
	writeFile(t, filepath.Join(dir, "b.db"), payload)

	a, err := addFile(source{abs: filepath.Join(dir, "a.db"), rel: "data/a.db"}, blocks)
	if err != nil {
		t.Fatalf("add a.db: %v", err)
	}

	b, err := addFile(source{abs: filepath.Join(dir, "b.db"), rel: "data/b.db"}, blocks)
	if err != nil {
		t.Fatalf("add b.db: %v", err)
	}

	if len(a.Blocks) != len(b.Blocks) || len(a.Blocks) == 0 {
		t.Fatalf("identical files must produce identical block lists: %d vs %d", len(a.Blocks), len(b.Blocks))
	}

	if len(blocks) != len(a.Blocks) {
		t.Fatalf("stored %d blocks for two identical files of %d blocks each", len(blocks), len(a.Blocks))
	}
}
