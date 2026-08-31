package netinfo

import (
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// observation is one FQDN seen in a reverse proxy header.
type observation struct {
	host string
	at   time.Time
}

// learner holds the observation history the wildcard and base domain are
// inferred from.
type learner struct {
	mu           sync.RWMutex
	observations []observation
	lastHost     string
	lastProto    string
}

// defaultLearner is the process-wide learner.
var defaultLearner = &learner{}

// now is the clock, replaced in tests to exercise the sample window.
var now = time.Now

// Observe records an FQDN seen in a trusted reverse proxy header and logs
// domain and protocol changes. It is a no-op when learning is disabled or
// when the DOMAIN list already pins the domains explicitly.
func Observe(host, proto string) {
	opts := Settings()
	if !opts.Learning || len(DomainList()) > 1 {
		return
	}

	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return
	}

	defaultLearner.observe(host, proto, opts)
}

// observe appends an observation, prunes the window, and reports changes.
func (l *learner) observe(host, proto string, opts Options) {
	l.mu.Lock()

	timestamp := now()
	l.observations = append(l.observations, observation{host: host, at: timestamp})
	l.prune(timestamp, opts.SampleWindow)

	previousHost := l.lastHost
	previousProto := l.lastProto
	l.lastHost = host
	if proto != "" {
		l.lastProto = proto
	}

	l.mu.Unlock()

	if !opts.LogChanges {
		return
	}

	if previousHost != "" && previousHost != host {
		if net.ParseIP(previousHost) != nil {
			Logf("fqdn upgraded from address %s to domain %s", previousHost, host)
		} else if BaseDomainOf(previousHost) != BaseDomainOf(host) {
			Logf("conflicting fqdn detected: %s then %s, using most recent", previousHost, host)
		} else {
			Logf("fqdn changed from %s to %s", previousHost, host)
		}
	}
	if proto != "" && previousProto != "" && previousProto != proto {
		Logf("proto changed from %s to %s", previousProto, proto)
	}
}

// prune drops observations older than the sample window. The caller holds
// the write lock.
func (l *learner) prune(timestamp time.Time, window time.Duration) {
	if window <= 0 {
		return
	}
	cutoff := timestamp.Add(-window)
	kept := l.observations[:0]
	for _, obs := range l.observations {
		if obs.at.After(cutoff) {
			kept = append(kept, obs)
		}
	}
	l.observations = kept
}

// snapshot returns the live observations inside the sample window.
func (l *learner) snapshot(window time.Duration) []observation {
	l.mu.RLock()
	defer l.mu.RUnlock()

	cutoff := now().Add(-window)
	out := make([]observation, 0, len(l.observations))
	for _, obs := range l.observations {
		if window <= 0 || obs.at.After(cutoff) {
			out = append(out, obs)
		}
	}
	return out
}

// GetBaseDomain returns the inferred registrable domain, so a request that
// arrived on www.myapp.com still yields myapp.com. The configured DOMAIN
// list wins when it is set.
func GetBaseDomain() string {
	opts := Settings()

	if domain := PrimaryDomain(); domain != "" {
		if base := BaseDomainOf(domain); base != "" {
			return base
		}
		return domain
	}

	counts := map[string]int{}
	for _, obs := range defaultLearner.snapshot(opts.SampleWindow) {
		if base := BaseDomainOf(obs.host); base != "" {
			counts[base]++
		}
	}
	return mostFrequent(counts)
}

// GetWildcardDomain returns the inferred wildcard pattern, or an empty
// string when no wildcard has been observed. A wildcard needs at least
// MinSamples observations and more than one distinct name on the same base
// domain.
func GetWildcardDomain() string {
	opts := Settings()

	if domains := DomainList(); len(domains) > 1 {
		base := BaseDomainOf(domains[0])
		if base == "" {
			return ""
		}
		for _, domain := range domains[1:] {
			if BaseDomainOf(domain) == base && domain != base {
				return "*." + base
			}
		}
		return ""
	}

	observations := defaultLearner.snapshot(opts.SampleWindow)
	if len(observations) < opts.MinSamples {
		return ""
	}

	base := GetBaseDomain()
	if base == "" {
		return ""
	}

	distinct := map[string]bool{}
	for _, obs := range observations {
		if BaseDomainOf(obs.host) == base {
			distinct[obs.host] = true
		}
	}
	if len(distinct) < 2 {
		return ""
	}
	return "*." + base
}

// PrimaryFQDN returns the learned primary name: the most frequent
// non-www host on the base domain, falling back to the base domain and
// then to plain detection.
func PrimaryFQDN() string {
	opts := Settings()

	if domain := PrimaryDomain(); domain != "" {
		return domain
	}

	base := GetBaseDomain()
	counts := map[string]int{}
	for _, obs := range defaultLearner.snapshot(opts.SampleWindow) {
		if strings.HasPrefix(obs.host, "www.") {
			continue
		}
		if base != "" && BaseDomainOf(obs.host) != base {
			continue
		}
		counts[obs.host]++
	}

	if host := mostFrequent(counts); host != "" {
		return host
	}
	if base != "" {
		return base
	}
	return DetectFQDN()
}

// ObservedHosts returns the distinct hosts seen inside the sample window,
// sorted for stable output. CORS uses it to widen the allow-list.
func ObservedHosts() []string {
	distinct := map[string]bool{}
	for _, obs := range defaultLearner.snapshot(Settings().SampleWindow) {
		distinct[obs.host] = true
	}

	out := make([]string, 0, len(distinct))
	for host := range distinct {
		out = append(out, host)
	}
	sort.Strings(out)
	return out
}

// ResetLearning clears the observation history, which a configuration
// reload does before applying new settings.
func ResetLearning() {
	defaultLearner.mu.Lock()
	defaultLearner.observations = nil
	defaultLearner.lastHost = ""
	defaultLearner.lastProto = ""
	defaultLearner.mu.Unlock()
}

// mostFrequent returns the highest-count key, breaking ties alphabetically
// so the result is deterministic.
func mostFrequent(counts map[string]int) string {
	best := ""
	bestCount := 0
	for key, count := range counts {
		if count > bestCount || (count == bestCount && key < best) {
			best = key
			bestCount = count
		}
	}
	return best
}
