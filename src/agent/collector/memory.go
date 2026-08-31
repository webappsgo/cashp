package collector

import (
	"strings"

	"github.com/webappsgo/cashp/src/agent/transport"
)

// ProcMeminfo is the kernel's memory accounting file.
const ProcMeminfo = "/proc/meminfo"

// MemInfo holds the memory figures the panel charts, in bytes.
type MemInfo struct {
	Total     float64
	Available float64
	Free      float64
	SwapTotal float64
	SwapFree  float64
}

// CollectMemory reports total, available and swap memory. Platforms
// without /proc report nothing rather than guessing.
func CollectMemory() ([]transport.MetricSample, error) {
	lines, err := readLines(ProcMeminfo)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, nil
	}

	info := ParseMeminfo(lines)
	samples := []transport.MetricSample{
		sample("memory.total", info.Total, "bytes", nil),
		sample("memory.available", info.Available, "bytes", nil),
		sample("memory.free", info.Free, "bytes", nil),
		sample("memory.swap_total", info.SwapTotal, "bytes", nil),
		sample("memory.swap_free", info.SwapFree, "bytes", nil),
	}
	if info.Total > 0 {
		used := (info.Total - info.Available) / info.Total * 100
		samples = append(samples, sample("memory.used_percent", used, "percent", nil))
	}
	return samples, nil
}

// ParseMeminfo turns /proc/meminfo lines into byte counts. The file
// reports kibibytes, so every value is scaled.
func ParseMeminfo(lines []string) MemInfo {
	info := MemInfo{}

	for _, line := range lines {
		name, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		value, ok := parseFloat(fields[0])
		if !ok {
			continue
		}
		if len(fields) > 1 && strings.EqualFold(fields[1], "kB") {
			value *= 1024
		}

		switch name {
		case "MemTotal":
			info.Total = value
		case "MemAvailable":
			info.Available = value
		case "MemFree":
			info.Free = value
		case "SwapTotal":
			info.SwapTotal = value
		case "SwapFree":
			info.SwapFree = value
		}
	}

	// Older kernels omit MemAvailable; MemFree is the closest stand-in.
	if info.Available == 0 {
		info.Available = info.Free
	}
	return info
}
