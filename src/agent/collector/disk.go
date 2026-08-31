package collector

import (
	"strings"

	"github.com/webappsgo/cashp/src/agent/transport"
)

// ProcMounts lists the currently mounted filesystems.
const ProcMounts = "/proc/mounts"

// virtualFilesystems are kernel and memory-backed filesystems whose usage
// figures say nothing about the node's actual storage.
var virtualFilesystems = map[string]bool{
	"autofs":      true,
	"binfmt_misc": true,
	"bpf":         true,
	"cgroup":      true,
	"cgroup2":     true,
	"configfs":    true,
	"debugfs":     true,
	"devpts":      true,
	"devtmpfs":    true,
	"fusectl":     true,
	"hugetlbfs":   true,
	"mqueue":      true,
	"overlay":     true,
	"proc":        true,
	"pstore":      true,
	"ramfs":       true,
	"securityfs":  true,
	"squashfs":    true,
	"sysfs":       true,
	"tmpfs":       true,
	"tracefs":     true,
}

// Usage is the space accounting for one mount point, in bytes.
type Usage struct {
	MountPoint string
	Total      float64
	Free       float64
}

// CollectDisk reports space usage for every real filesystem on the node.
// Platforms where usage cannot be measured report nothing rather than
// sending zeroes the panel would chart as a full disk.
func CollectDisk() ([]transport.MetricSample, error) {
	lines, err := readLines(ProcMounts)
	if err != nil {
		return nil, err
	}

	mounts := ParseMounts(lines)
	if len(mounts) == 0 {
		mounts = DefaultMountPoints()
	}

	samples := []transport.MetricSample{}
	for _, mount := range mounts {
		usage, ok := diskUsage(mount)
		if !ok || usage.Total <= 0 {
			continue
		}

		labels := map[string]string{"mount": mount}
		used := (usage.Total - usage.Free) / usage.Total * 100
		samples = append(samples,
			sample("disk.total", usage.Total, "bytes", labels),
			sample("disk.free", usage.Free, "bytes", labels),
			sample("disk.used_percent", used, "percent", labels),
		)
	}
	return samples, nil
}

// ParseMounts extracts the real filesystem mount points from /proc/mounts
// lines, skipping kernel and memory-backed filesystems.
func ParseMounts(lines []string) []string {
	mounts := []string{}
	seen := map[string]bool{}

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if virtualFilesystems[fields[2]] {
			continue
		}

		point := unescapeMount(fields[1])
		if point == "" || seen[point] {
			continue
		}
		seen[point] = true
		mounts = append(mounts, point)
	}
	return mounts
}

// unescapeMount decodes the octal escapes the kernel writes for spaces,
// tabs, newlines and backslashes in mount paths.
func unescapeMount(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}

	replacer := strings.NewReplacer(
		`\040`, " ",
		`\011`, "\t",
		`\012`, "\n",
		`\134`, `\`,
	)
	return replacer.Replace(value)
}
