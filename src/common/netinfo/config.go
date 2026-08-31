package netinfo

import (
	"strings"
	"sync"
	"time"
)

// Options are the runtime settings URL resolution depends on. They are
// supplied by the caller after the configuration file is loaded, so this
// package never imports the config package.
type Options struct {
	// DevMode relaxes FQDN validation to allow dev-only TLDs.
	DevMode bool
	// ProjectName is the internal project name, which doubles as a
	// dev-only TLD (for example app.cashp).
	ProjectName string
	// ListenAddress is the interface the server bound to.
	ListenAddress string
	// ListenPort is the port the server actually listens on.
	ListenPort string
	// OnionAddress is the Tor hidden service address when enabled.
	OnionAddress string
	// I2PAddress is the I2P eepsite address when enabled.
	I2PAddress string
	// Domains is the DOMAIN list; the first entry is primary.
	Domains []string
	// Learning enables smart FQDN learning from proxy headers.
	Learning bool
	// MinSamples is the number of observations before a wildcard is
	// inferred.
	MinSamples int
	// SampleWindow is the period observations are retained for.
	SampleWindow time.Duration
	// LogChanges enables logging of learned domain changes.
	LogChanges bool
	// LiveReload allows URL variables to update without a restart.
	LiveReload bool
}

// DefaultOptions returns the documented sane defaults: learning on, three
// samples, a five minute window, change logging on, live reload on.
func DefaultOptions() Options {
	return Options{
		ProjectName:  "cashp",
		Learning:     true,
		MinSamples:   3,
		SampleWindow: 5 * time.Minute,
		LogChanges:   true,
		LiveReload:   true,
	}
}

// optionsMu guards the active settings, which a config reload replaces.
var (
	optionsMu sync.RWMutex
	options   = DefaultOptions()
)

// Logf receives domain change notices. It is a no-op until the caller wires
// the application logger, keeping this package free of logging imports.
var Logf = func(format string, args ...any) {}

// Configure replaces the active settings. Zero values for MinSamples and
// SampleWindow fall back to the documented defaults.
func Configure(opts Options) {
	defaults := DefaultOptions()
	if opts.ProjectName == "" {
		opts.ProjectName = defaults.ProjectName
	}
	if opts.MinSamples <= 0 {
		opts.MinSamples = defaults.MinSamples
	}
	if opts.SampleWindow <= 0 {
		opts.SampleWindow = defaults.SampleWindow
	}
	opts.Domains = cleanDomains(opts.Domains)

	optionsMu.Lock()
	options = opts
	optionsMu.Unlock()
}

// Settings returns a copy of the active settings.
func Settings() Options {
	optionsMu.RLock()
	defer optionsMu.RUnlock()

	out := options
	out.Domains = make([]string, len(options.Domains))
	copy(out.Domains, options.Domains)
	return out
}

// cleanDomains trims and lowercases a domain list, dropping empty entries.
func cleanDomains(domains []string) []string {
	out := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain != "" {
			out = append(out, domain)
		}
	}
	return out
}
