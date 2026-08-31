package admin

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// hostStats is the real runtime and host information the dashboard and the
// server-info page display. Every value is measured, never estimated, and no
// filesystem path is included.
type hostStats struct {
	Hostname     string
	Uptime       time.Duration
	StartedAt    time.Time
	GoVersion    string
	OS           string
	Arch         string
	NumCPU       int
	Goroutines   int
	HeapAlloc    int64
	HeapSys      int64
	StackSys     int64
	GCRuns       uint32
	MemTotal     int64
	MemAvailable int64
	MemUsedPct   int
	Load1        string
	Load5        string
	Load15       string
	DBDriver     string
	DBOpen       int
	DBInUse      int
	DBIdle       int
	DBWaitCount  int64
	DBHealthy    bool
}

// hostStats collects the current process and host measurements.
func (p *Panel) hostStats(ctx context.Context) hostStats {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	stats := hostStats{
		Uptime:     time.Since(p.startedAt).Truncate(time.Second),
		StartedAt:  p.startedAt,
		GoVersion:  runtime.Version(),
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		NumCPU:     runtime.NumCPU(),
		Goroutines: runtime.NumGoroutine(),
		HeapAlloc:  int64(mem.HeapAlloc),
		HeapSys:    int64(mem.HeapSys),
		StackSys:   int64(mem.StackSys),
		GCRuns:     mem.NumGC,
	}

	// The hostname is the server's own name, which an administrator is entitled
	// to see; it is never rendered on a public page.
	if name, err := os.Hostname(); err == nil {
		stats.Hostname = name
	}

	stats.MemTotal, stats.MemAvailable = readMemInfo()
	if stats.MemTotal > 0 && stats.MemAvailable <= stats.MemTotal {
		used := stats.MemTotal - stats.MemAvailable
		stats.MemUsedPct = int(used * 100 / stats.MemTotal)
	}
	stats.Load1, stats.Load5, stats.Load15 = readLoadAverage()

	pool := p.db.Stats()
	stats.DBDriver = p.db.Driver()
	stats.DBOpen = pool.OpenConnections
	stats.DBInUse = pool.InUse
	stats.DBIdle = pool.Idle
	stats.DBWaitCount = pool.WaitCount
	stats.DBHealthy = p.db.Ping(ctx) == nil

	return stats
}

// readMemInfo returns total and available physical memory in bytes. Both are
// zero on a host that does not publish /proc/meminfo.
func readMemInfo() (total, available int64) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch name {
		case "MemTotal":
			total = kb * 1024
		case "MemAvailable":
			available = kb * 1024
		}
		if total > 0 && available > 0 {
			break
		}
	}
	return total, available
}

// readLoadAverage returns the one, five and fifteen minute load averages. The
// values are empty on a host without /proc/loadavg.
func readLoadAverage() (one, five, fifteen string) {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "", "", ""
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 3 {
		return "", "", ""
	}
	return fields[0], fields[1], fields[2]
}
