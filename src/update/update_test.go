package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/notify"
)

// fixedNow is the clock every test runs against, so defer windows are
// deterministic.
var fixedNow = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// daysAgo returns a timestamp the given number of days before fixedNow.
func daysAgo(d int) time.Time {
	return fixedNow.AddDate(0, 0, -d)
}

// wireRelease mirrors the GitHub Releases API JSON used by the fake feed.
type wireRelease struct {
	TagName     string    `json:"tag_name"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Body        string    `json:"body"`
	Assets      []Asset   `json:"assets"`
}

// feedServer serves /releases and /releases/latest from the given list.
func feedServer(t *testing.T, releases []wireRelease, latest *wireRelease) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if latest == nil {
			w.WriteHeader(http.StatusNotFound)

			return
		}
		writeJSON(t, w, latest)
	})
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, releases)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

// writeJSON encodes v as the response body.
func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

// newTestService builds a Service pointed at srv with the frozen clock.
func newTestService(srv *httptest.Server, opts Options) *Service {
	s := New(opts)
	s.apiBase = srv.URL
	s.client = srv.Client()
	s.now = func() time.Time { return fixedNow }

	return s
}

func TestClassify(t *testing.T) {
	cases := []struct {
		tag        string
		prerelease bool
		want       Channel
	}{
		{"v1.0.0", false, ChannelStable},
		{"1.0.0", false, ChannelStable},
		{"202512051430-beta", true, ChannelBeta},
		{"daily", true, ChannelDaily},
		{"v2.0.0-rc1", true, ChannelBeta},
	}

	for _, c := range cases {
		if got := classify(c.tag, c.prerelease); got != c.want {
			t.Errorf("classify(%q, %v) = %q, want %q", c.tag, c.prerelease, got, c.want)
		}
	}
}

func TestMatchesChannelIsCumulative(t *testing.T) {
	cases := []struct {
		rel  Channel
		want Channel
		ok   bool
	}{
		{ChannelStable, ChannelStable, true},
		{ChannelBeta, ChannelStable, false},
		{ChannelDaily, ChannelStable, false},
		{ChannelStable, ChannelBeta, true},
		{ChannelBeta, ChannelBeta, true},
		{ChannelDaily, ChannelBeta, false},
		{ChannelStable, ChannelDaily, true},
		{ChannelBeta, ChannelDaily, true},
		{ChannelDaily, ChannelDaily, true},
	}

	for _, c := range cases {
		if got := matchesChannel(c.rel, c.want); got != c.ok {
			t.Errorf("matchesChannel(%q, %q) = %v, want %v", c.rel, c.want, got, c.ok)
		}
	}
}

func TestNewNormalizesOptions(t *testing.T) {
	s := New(Options{Channel: "nightly", DeferDays: 900})
	if s.Options().Channel != ChannelStable {
		t.Errorf("unknown channel = %q, want stable", s.Options().Channel)
	}
	if s.Options().DeferDays != maxDeferDays {
		t.Errorf("defer days = %d, want %d", s.Options().DeferDays, maxDeferDays)
	}
	if s.AutoInstall() {
		t.Error("auto_install must default to false")
	}

	if New(Options{DeferDays: -5}).Options().DeferDays != 0 {
		t.Error("negative defer days must clamp to 0")
	}
}

func TestCheckStableUsesLatestEndpoint(t *testing.T) {
	latest := wireRelease{TagName: "v1.3.0", PublishedAt: daysAgo(1)}
	srv := feedServer(t, nil, &latest)
	s := newTestService(srv, Options{Channel: ChannelStable, CurrentVersion: "v1.2.0"})

	rel, ok, err := s.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !ok || rel.Version != "v1.3.0" {
		t.Fatalf("Check = %q, %v; want v1.3.0, true", rel.Version, ok)
	}
	if rel.Channel != ChannelStable {
		t.Errorf("channel = %q, want stable", rel.Channel)
	}
}

func TestCheckStableAlreadyCurrent(t *testing.T) {
	latest := wireRelease{TagName: "v1.3.0", PublishedAt: daysAgo(1)}
	srv := feedServer(t, nil, &latest)
	s := newTestService(srv, Options{Channel: ChannelStable, CurrentVersion: "1.3.0"})

	_, ok, err := s.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if ok {
		t.Error("running the latest release must report no update")
	}
}

func TestCheckNotFoundMeansNoUpdate(t *testing.T) {
	srv := feedServer(t, nil, nil)
	s := newTestService(srv, Options{Channel: ChannelStable, CurrentVersion: "v1.0.0"})

	_, ok, err := s.Check(context.Background())
	if err != nil {
		t.Fatalf("HTTP 404 must not be an error: %v", err)
	}
	if ok {
		t.Error("HTTP 404 must report no update available")
	}
}

func TestBetaChannelNeverStuckBehindStable(t *testing.T) {
	releases := []wireRelease{
		{TagName: "202512051430-beta", Prerelease: true, PublishedAt: daysAgo(10)},
		{TagName: "v1.3.0", PublishedAt: daysAgo(2)},
		{TagName: "v1.2.0", PublishedAt: daysAgo(60)},
	}
	srv := feedServer(t, releases, nil)
	s := newTestService(srv, Options{Channel: ChannelBeta, CurrentVersion: "v1.2.0"})

	rel, ok, err := s.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !ok || rel.Version != "v1.3.0" {
		t.Fatalf("beta channel = %q, %v; want the newer stable v1.3.0", rel.Version, ok)
	}
}

func TestStableChannelIgnoresPrereleases(t *testing.T) {
	releases := []wireRelease{
		{TagName: "daily", Prerelease: true, PublishedAt: daysAgo(1)},
		{TagName: "202512051430-beta", Prerelease: true, PublishedAt: daysAgo(2)},
		{TagName: "v1.2.0", PublishedAt: daysAgo(60)},
	}
	srv := feedServer(t, releases, nil)
	// A defer window forces the stable channel down the full-feed path,
	// where prereleases are visible and must still be filtered out.
	s := newTestService(srv, Options{Channel: ChannelStable, DeferDays: 1, CurrentVersion: "v1.2.0"})

	_, ok, err := s.ScheduledCheck(context.Background())
	if err != nil {
		t.Fatalf("ScheduledCheck: %v", err)
	}
	if ok {
		t.Error("stable channel must not select a prerelease")
	}
}

func TestDailyChannelComparesBuildEpoch(t *testing.T) {
	published := daysAgo(1)
	releases := []wireRelease{
		{TagName: "daily", Prerelease: true, PublishedAt: published},
		{TagName: "v1.2.0", PublishedAt: daysAgo(60)},
	}
	srv := feedServer(t, releases, nil)

	stale := newTestService(srv, Options{Channel: ChannelDaily, CurrentEpoch: published.Unix() - 1})
	rel, ok, err := stale.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !ok || rel.Version != "daily" {
		t.Fatalf("older build = %q, %v; want daily, true", rel.Version, ok)
	}

	current := newTestService(srv, Options{Channel: ChannelDaily, CurrentEpoch: published.Unix()})
	if _, ok, err := current.Check(context.Background()); err != nil || ok {
		t.Fatalf("build newer than the rolling daily must report no update (ok=%v err=%v)", ok, err)
	}
}

func TestDeferDaysGatesScheduledCheckOnly(t *testing.T) {
	releases := []wireRelease{
		{TagName: "v1.2.2", PublishedAt: daysAgo(100)},
		{TagName: "v1.2.3", PublishedAt: daysAgo(40)},
		{TagName: "v1.2.4", PublishedAt: daysAgo(5)},
	}
	latest := releases[2]
	srv := feedServer(t, releases, &latest)
	s := newTestService(srv, Options{Channel: ChannelStable, DeferDays: 30, CurrentVersion: "v1.2.2"})

	rel, ok, err := s.ScheduledCheck(context.Background())
	if err != nil {
		t.Fatalf("ScheduledCheck: %v", err)
	}
	if !ok || rel.Version != "v1.2.3" {
		t.Fatalf("scheduled check = %q, %v; want v1.2.3 (v1.2.4 is inside the defer window)", rel.Version, ok)
	}

	rel, ok, err = s.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !ok || rel.Version != "v1.2.4" {
		t.Fatalf("manual check = %q, %v; want the true latest v1.2.4", rel.Version, ok)
	}
}

func TestScheduledCheckWithNoEligibleRelease(t *testing.T) {
	releases := []wireRelease{
		{TagName: "v1.2.2", PublishedAt: daysAgo(100)},
		{TagName: "v1.2.3", PublishedAt: daysAgo(2)},
	}
	srv := feedServer(t, releases, nil)
	s := newTestService(srv, Options{Channel: ChannelStable, DeferDays: 30, CurrentVersion: "v1.2.2"})

	if _, ok, err := s.ScheduledCheck(context.Background()); err != nil || ok {
		t.Fatalf("release inside the defer window must be skipped (ok=%v err=%v)", ok, err)
	}
}

// newTestNotifier builds a real *notify.Notifier backed by a throwaway
// SQLite database, so a dispatch can be observed through the store's dedup
// claims without a live SMTP server or webhook target.
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

func TestScheduledCheckWithoutNotifierSkipsNotification(t *testing.T) {
	releases := []wireRelease{{TagName: "v1.2.4", PublishedAt: daysAgo(5)}}
	latest := releases[0]
	srv := feedServer(t, releases, &latest)
	s := newTestService(srv, Options{Channel: ChannelStable, CurrentVersion: "v1.2.2"})

	if _, ok, err := s.ScheduledCheck(context.Background()); err != nil || !ok {
		t.Fatalf("scheduled check = %v, %v; want an eligible release", ok, err)
	}
}

func TestScheduledCheckNotifiesUpdateAvailable(t *testing.T) {
	releases := []wireRelease{{TagName: "v1.2.4", PublishedAt: daysAgo(5)}}
	latest := releases[0]
	srv := feedServer(t, releases, &latest)
	s := newTestService(srv, Options{Channel: ChannelStable, CurrentVersion: "v1.2.2"})
	s.opts.Notifier = newTestNotifier(t)
	ctx := context.Background()

	if _, ok, err := s.ScheduledCheck(ctx); err != nil || !ok {
		t.Fatalf("scheduled check = %v, %v; want an eligible release", ok, err)
	}

	held, err := s.opts.Notifier.Store().DedupHeld(ctx, notify.EventUpdateAvailable+":")
	if err != nil {
		t.Fatalf("dedup held: %v", err)
	}
	if !held {
		t.Fatal("expected update_available to have been dispatched")
	}
}

func TestCheckAPIErrorIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	s := newTestService(srv, Options{Channel: ChannelStable})
	if _, _, err := s.Check(context.Background()); err == nil {
		t.Fatal("HTTP 500 must be reported as an error")
	}
}

func TestAssetNameForEachBinary(t *testing.T) {
	for _, name := range []string{"cashp", "cashp-cli", "cashp-agent"} {
		got := assetNameFor(filepath.Join("/usr/local/bin", name))
		if !strings.HasPrefix(got, name+"-") {
			t.Errorf("assetNameFor(%q) = %q, want a %q-prefixed asset", name, got, name)
		}
	}
}
