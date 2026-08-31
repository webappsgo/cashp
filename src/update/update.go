// Package update implements cashp's self-update support per AI.md PART 23.
// It resolves the newest release for a cumulative channel (stable, beta,
// daily) from the GitHub Releases API, honours the scheduled task's
// defer_days window, and installs verified binaries in place. All three
// shipped binaries (cashp, cashp-cli, cashp-agent) are self-updatable
// through the same service.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/logging"
	"github.com/webappsgo/cashp/src/notify"
)

// Channel is a release channel. Channels are cumulative: beta considers
// beta and stable releases, daily considers daily, beta and stable, so a
// less-stable channel never leaves the operator older than a more-stable
// one.
type Channel string

const (
	// ChannelStable considers full releases only (v*, *.*.*).
	ChannelStable Channel = "stable"
	// ChannelBeta considers *-beta pre-releases plus every stable release.
	ChannelBeta Channel = "beta"
	// ChannelDaily considers the rolling daily pre-release plus beta and
	// stable releases.
	ChannelDaily Channel = "daily"
)

// dailyTag is the rolling pre-release tag that is deleted and recreated
// nightly, so it never changes value between builds.
const dailyTag = "daily"

// checksumAsset is the release asset that must carry the SHA256 digests.
// A release without it is refused rather than installed unverified.
const checksumAsset = "sha256.txt"

// defaultAPIBase is the GitHub API repository root for the release feed.
const defaultAPIBase = "https://api.github.com/repos/webappsgo/cashp"

// maxDeferDays is the upper bound accepted for update.defer_days.
const maxDeferDays = 365

// userAgent identifies the updater to the GitHub API, which rejects
// requests that send no User-Agent at all.
const userAgent = "cashp-updater"

// Options configures a Service. CurrentVersion is the running release tag
// and CurrentEpoch the build timestamp used to compare against the rolling
// daily release, whose tag never changes. BinaryPath is the executable to
// replace; empty means the running executable.
type Options struct {
	Channel        Channel
	DeferDays      int
	AutoInstall    bool
	CurrentVersion string
	CurrentEpoch   int64
	BinaryPath     string
	// Notifier delivers update_available/update_installed notifications per
	// AI.md PART 18's decision matrix; nil disables notification entirely.
	Notifier *notify.Notifier
}

// Asset is a downloadable file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is a single published release, normalised from the GitHub API
// representation. Epoch is PublishedAt as a Unix timestamp.
type Release struct {
	Version     string
	Channel     Channel
	PublishedAt time.Time
	Epoch       int64
	Assets      []Asset
	Notes       string
}

// Service resolves and installs updates for one binary.
type Service struct {
	opts    Options
	client  *http.Client
	apiBase string
	now     func() time.Time
}

// githubRelease is the wire shape returned by the GitHub Releases API.
type githubRelease struct {
	TagName     string    `json:"tag_name"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Body        string    `json:"body"`
	Assets      []Asset   `json:"assets"`
}

// New builds a Service from opts, normalising the channel and clamping
// defer_days to the documented 0-365 range.
func New(opts Options) *Service {
	switch opts.Channel {
	case ChannelStable, ChannelBeta, ChannelDaily:
	default:
		opts.Channel = ChannelStable
	}

	if opts.DeferDays < 0 {
		opts.DeferDays = 0
	}
	if opts.DeferDays > maxDeferDays {
		opts.DeferDays = maxDeferDays
	}

	return &Service{
		opts:    opts,
		client:  &http.Client{},
		apiBase: defaultAPIBase,
		now:     time.Now,
	}
}

// Options returns the normalised options the Service was built with.
func (s *Service) Options() Options {
	return s.opts
}

// AutoInstall reports whether the scheduled update_check task may install
// what it finds. It defaults to false: the task notifies only and
// installing stays an explicit operator action.
func (s *Service) AutoInstall() bool {
	return s.opts.AutoInstall
}

// Check resolves the true newest release for the configured channel. It is
// the manual --update / --update check path and deliberately ignores
// DeferDays: an explicit operator action always sees the latest release.
func (s *Service) Check(ctx context.Context) (Release, bool, error) {
	return s.resolve(ctx, 0)
}

// ScheduledCheck resolves the newest release that has already aged past
// the DeferDays window, for the scheduled update_check task. Whether the
// result is merely announced or installed is decided by AutoInstall, which
// is false by default.
func (s *Service) ScheduledCheck(ctx context.Context) (Release, bool, error) {
	rel, ok, err := s.resolve(ctx, time.Duration(s.opts.DeferDays)*24*time.Hour)
	if err == nil && ok {
		s.notify(ctx, notify.EventUpdateAvailable, map[string]string{
			"current_version": s.opts.CurrentVersion,
			"new_version":     rel.Version,
			"channel":         string(rel.Channel),
		})
	}
	return rel, ok, err
}

// notify dispatches one update event, tolerating both an absent notifier
// and a delivery failure - a notification is never allowed to fail the
// check or install it describes.
func (s *Service) notify(ctx context.Context, event string, vars map[string]string) {
	if s.opts.Notifier == nil {
		return
	}
	if err := s.opts.Notifier.Notify(ctx, notify.Message{Event: event, Vars: vars}); err != nil {
		logging.L().Warn("update notification failed", "event", event, "error", err.Error())
	}
}

// resolve fetches the release feed and selects the newest candidate that is
// at least minAge old. A zero minAge disables the defer filter entirely.
func (s *Service) resolve(ctx context.Context, minAge time.Duration) (Release, bool, error) {
	// The single-release /releases/latest endpoint cannot answer "newest
	// release older than N days", so the full feed is used whenever the
	// defer window is active.
	if s.opts.Channel == ChannelStable && minAge == 0 {
		rel, ok, err := s.fetchLatestStable(ctx)
		if err != nil || !ok {
			return Release{}, false, err
		}
		if !s.isNewer(rel, nil) {
			return Release{}, false, nil
		}
		return rel, true, nil
	}

	releases, err := s.fetchReleases(ctx)
	if err != nil {
		return Release{}, false, err
	}

	rel, ok := s.selectRelease(releases, minAge)

	return rel, ok, nil
}

// selectRelease picks the newest release matching the configured channel
// that is eligible under minAge and strictly newer than the running build.
// Eligibility is evaluated per release, so a brand-new publish never resets
// the defer clock for an older release that has already aged past it.
func (s *Service) selectRelease(releases []Release, minAge time.Duration) (Release, bool) {
	now := s.now().UTC()

	var currentPublished *time.Time
	for i := range releases {
		if s.opts.CurrentVersion != "" && sameVersion(releases[i].Version, s.opts.CurrentVersion) {
			published := releases[i].PublishedAt
			currentPublished = &published

			break
		}
	}

	var newest *Release
	for i := range releases {
		r := &releases[i]
		if !matchesChannel(r.Channel, s.opts.Channel) {
			continue
		}
		if minAge > 0 && now.Sub(r.PublishedAt.UTC()) < minAge {
			continue
		}
		if newest == nil || r.PublishedAt.After(newest.PublishedAt) {
			newest = r
		}
	}

	if newest == nil {
		return Release{}, false
	}
	if !s.isNewer(*newest, currentPublished) {
		return Release{}, false
	}

	return *newest, true
}

// fetchLatestStable reads /releases/latest, the endpoint that returns the
// newest full release. HTTP 404 means the repository has no release yet,
// which is reported as "no update available" rather than an error.
func (s *Service) fetchLatestStable(ctx context.Context) (Release, bool, error) {
	body, ok, err := s.get(ctx, s.apiBase+"/releases/latest")
	if err != nil || !ok {
		return Release{}, false, err
	}

	var wire githubRelease
	if err := json.Unmarshal(body, &wire); err != nil {
		return Release{}, false, fmt.Errorf("decode latest release: %w", err)
	}

	rel := normalize(wire)
	if rel.Channel != ChannelStable {
		return Release{}, false, nil
	}

	return rel, true, nil
}

// fetchReleases reads the full /releases feed used by the cumulative beta
// and daily channels and by any defer-filtered lookup.
func (s *Service) fetchReleases(ctx context.Context) ([]Release, error) {
	body, ok, err := s.get(ctx, s.apiBase+"/releases")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	var wire []githubRelease
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}

	out := make([]Release, 0, len(wire))
	for _, w := range wire {
		out = append(out, normalize(w))
	}

	return out, nil
}

// get performs an authenticated-free GET against the GitHub API. The bool
// result is false for HTTP 404, which the spec defines as "no updates
// available (already current)".
func (s *Service) get(ctx context.Context, url string) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("github api error: %d", resp.StatusCode)
	}

	body, err := readCapped(resp.Body, maxFeedBytes)
	if err != nil {
		return nil, false, err
	}

	return body, true, nil
}

// normalize converts a GitHub API release into the package's own Release,
// classifying its channel from the tag and prerelease flag.
func normalize(w githubRelease) Release {
	return Release{
		Version:     w.TagName,
		Channel:     classify(w.TagName, w.Prerelease),
		PublishedAt: w.PublishedAt,
		Epoch:       w.PublishedAt.Unix(),
		Assets:      w.Assets,
		Notes:       w.Body,
	}
}

// classify maps a release tag onto its own channel: the rolling daily tag,
// a -beta pre-release, or a full stable release.
func classify(tag string, prerelease bool) Channel {
	if tag == dailyTag {
		return ChannelDaily
	}
	if strings.HasSuffix(tag, "-beta") {
		return ChannelBeta
	}
	if prerelease {
		return ChannelBeta
	}

	return ChannelStable
}

// matchesChannel implements cumulative channel membership: stable sees
// stable only, beta sees beta plus stable, daily sees everything.
func matchesChannel(rel Channel, want Channel) bool {
	switch want {
	case ChannelBeta:
		return rel == ChannelBeta || rel == ChannelStable
	case ChannelDaily:
		return rel == ChannelDaily || rel == ChannelBeta || rel == ChannelStable
	default:
		return rel == ChannelStable
	}
}

// sameVersion compares release tags ignoring a leading v, so v1.2.3 and
// 1.2.3 are recognised as the same release.
func sameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// isNewer reports whether rel should be offered over the running build.
// The rolling daily release keeps one tag forever, so it is compared
// against this binary's own build epoch instead of a tag.
func (s *Service) isNewer(rel Release, currentPublished *time.Time) bool {
	if rel.Version == dailyTag {
		return rel.Epoch > s.opts.CurrentEpoch
	}
	if s.opts.CurrentVersion != "" && sameVersion(rel.Version, s.opts.CurrentVersion) {
		return false
	}
	if currentPublished != nil && !rel.PublishedAt.After(*currentPublished) {
		return false
	}
	if currentPublished == nil && s.opts.CurrentEpoch > 0 && rel.Epoch <= s.opts.CurrentEpoch {
		return false
	}

	return true
}
