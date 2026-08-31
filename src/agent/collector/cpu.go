package collector

import (
	"runtime"
	"strings"

	"github.com/webappsgo/cashp/src/agent/transport"
)

// ProcStat is the kernel's aggregate CPU time accounting file.
const ProcStat = "/proc/stat"

// ProcLoadavg is the kernel's load-average file.
const ProcLoadavg = "/proc/loadavg"

// CPUTimes holds one snapshot of aggregate CPU time in jiffies.
type CPUTimes struct {
	Idle  float64
	Total float64
}

// CollectCPU reports core count, load averages and cumulative CPU busy
// time. On platforms without /proc it reports the core count only.
func CollectCPU() ([]transport.MetricSample, error) {
	samples := []transport.MetricSample{
		sample("cpu.cores", float64(runtime.NumCPU()), "count", nil),
	}

	loadLines, err := readLines(ProcLoadavg)
	if err != nil {
		return nil, err
	}
	if len(loadLines) > 0 {
		fields := strings.Fields(loadLines[0])
		names := []string{"cpu.load1", "cpu.load5", "cpu.load15"}
		for index, name := range names {
			if index >= len(fields) {
				break
			}
			if value, ok := parseFloat(fields[index]); ok {
				samples = append(samples, sample(name, value, "load", nil))
			}
		}
	}

	statLines, err := readLines(ProcStat)
	if err != nil {
		return nil, err
	}
	if times, ok := ParseCPUTimes(statLines); ok {
		samples = append(samples,
			sample("cpu.time_total", times.Total, "jiffies", nil),
			sample("cpu.time_idle", times.Idle, "jiffies", nil),
		)
		if times.Total > 0 {
			busy := (times.Total - times.Idle) / times.Total * 100
			samples = append(samples, sample("cpu.busy_percent", busy, "percent", nil))
		}
	}
	return samples, nil
}

// ParseCPUTimes extracts the aggregate "cpu" line from /proc/stat.
func ParseCPUTimes(lines []string) (CPUTimes, bool) {
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}

		times := CPUTimes{}
		for index, field := range fields[1:] {
			value, ok := parseFloat(field)
			if !ok {
				return CPUTimes{}, false
			}
			times.Total += value
			// Fields 3 and 4 (zero-based) are idle and iowait, both of
			// which count as time the CPU was not doing work.
			if index == 3 || index == 4 {
				times.Idle += value
			}
		}
		return times, true
	}
	return CPUTimes{}, false
}
