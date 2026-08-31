package metrics

import (
	"os"
	"strconv"
	"strings"
)

// cpuTimes is one sample of the aggregate CPU time counters. CPU usage is a
// rate, so it is only meaningful as the delta between two samples.
type cpuTimes struct {
	total float64
	idle  float64
	valid bool
}

// refreshSystem publishes the CPU, memory, and disk metrics gated by
// server.metrics.include_system. Each source is optional: on a platform
// where it is unavailable the corresponding metric is simply not updated.
func (r *Registry) refreshSystem() {
	if usage, ok := r.cpuUsagePercent(); ok {
		r.Gauge(MetricSystemCPUUsagePercent).Set(usage)
	}

	if used, total, ok := memoryUsage(); ok {
		r.Gauge(MetricSystemMemoryUsedBytes).Set(float64(used))
		r.Gauge(MetricSystemMemoryTotalBytes).Set(float64(total))
		if total > 0 {
			r.Gauge(MetricSystemMemoryUsagePercent).Set(float64(used) / float64(total) * 100)
		}
	}

	path := r.opts.DataDir
	if path == "" {
		return
	}

	if used, total, ok := diskUsage(path); ok {
		r.Gauge(MetricSystemDiskUsedBytes, "path", path).Set(float64(used))
		r.Gauge(MetricSystemDiskTotalBytes, "path", path).Set(float64(total))
		if total > 0 {
			r.Gauge(MetricSystemDiskUsagePercent, "path", path).Set(float64(used) / float64(total) * 100)
		}
	}
}

// cpuUsagePercent returns the busy percentage of CPU time elapsed since the
// previous call.
func (r *Registry) cpuUsagePercent() (float64, bool) {
	current, ok := readCPUTimes()
	if !ok {
		return 0, false
	}

	r.sysMu.Lock()
	previous := r.lastCPU
	r.lastCPU = current
	r.sysMu.Unlock()

	if !previous.valid {
		return 0, false
	}

	totalDelta := current.total - previous.total
	if totalDelta <= 0 {
		return 0, false
	}

	idleDelta := current.idle - previous.idle
	usage := (totalDelta - idleDelta) / totalDelta * 100

	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}

	return usage, true
}

// readCPUTimes parses the aggregate "cpu" line of /proc/stat.
func readCPUTimes() (cpuTimes, bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}, false
	}

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}

		var times cpuTimes
		for i, field := range fields[1:] {
			value, err := strconv.ParseFloat(field, 64)
			if err != nil {
				return cpuTimes{}, false
			}

			times.total += value

			// Fields 3 and 4 of the cpu line are idle and iowait; both
			// count as time the CPU was not doing work.
			if i == 3 || i == 4 {
				times.idle += value
			}
		}

		times.valid = true

		return times, true
	}

	return cpuTimes{}, false
}

// memoryUsage returns used and total system memory in bytes.
func memoryUsage() (used, total uint64, ok bool) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}

	var memTotal, memAvailable uint64
	var haveTotal, haveAvailable bool

	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}

		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}

		parsed, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}

		// /proc/meminfo reports kibibytes.
		parsed *= 1024

		switch key {
		case "MemTotal":
			memTotal, haveTotal = parsed, true
		case "MemAvailable":
			memAvailable, haveAvailable = parsed, true
		}
	}

	if !haveTotal || !haveAvailable || memAvailable > memTotal {
		return 0, 0, false
	}

	return memTotal - memAvailable, memTotal, true
}
