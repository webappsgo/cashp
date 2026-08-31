// Package collector gathers the node metrics the agent reports to the
// panel. Every collector is pure Go and reads only well-known system
// interfaces, so the agent needs no cgo and no external helper binaries.
package collector

import (
	"os"
	"strconv"
	"strings"

	"github.com/webappsgo/cashp/src/agent/transport"
)

// Source is one named group of measurements.
type Source struct {
	// Name identifies the source in status output.
	Name string
	// Collect returns the samples for this cycle. A source that cannot run
	// on this platform returns no samples and no error.
	Collect func() ([]transport.MetricSample, error)
}

// Sources is every collector the agent runs each cycle.
func Sources() []Source {
	return []Source{
		{Name: "cpu", Collect: CollectCPU},
		{Name: "memory", Collect: CollectMemory},
		{Name: "disk", Collect: CollectDisk},
		{Name: "network", Collect: CollectNetwork},
	}
}

// Collect runs every source and returns all samples. A source that fails
// is skipped and named in the returned error list rather than aborting the
// cycle: partial metrics are more useful than none.
func Collect() ([]transport.MetricSample, []error) {
	samples := []transport.MetricSample{}
	failures := []error{}

	for _, source := range Sources() {
		collected, err := source.Collect()
		if err != nil {
			failures = append(failures, err)
			continue
		}
		samples = append(samples, collected...)
	}
	return samples, failures
}

// readLines reads a system file into trimmed, non-empty lines. A missing
// file simply yields no lines, which is how non-Linux platforms fall
// through to reporting nothing for a source.
func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	lines := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines, nil
}

// parseFloat parses a numeric field, reporting whether it was valid.
func parseFloat(value string) (float64, bool) {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// sample builds one measurement.
func sample(name string, value float64, unit string, labels map[string]string) transport.MetricSample {
	return transport.MetricSample{Name: name, Value: value, Unit: unit, Labels: labels}
}
