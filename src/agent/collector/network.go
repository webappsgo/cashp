package collector

import (
	"strings"

	"github.com/webappsgo/cashp/src/agent/transport"
)

// ProcNetDev is the kernel's per-interface traffic counter file.
const ProcNetDev = "/proc/net/dev"

// InterfaceStats holds the cumulative counters for one network interface.
type InterfaceStats struct {
	Name      string
	RxBytes   float64
	RxPackets float64
	RxErrors  float64
	TxBytes   float64
	TxPackets float64
	TxErrors  float64
}

// skippedInterfacePrefixes are virtual interfaces whose counters duplicate
// or obscure the node's real network activity.
var skippedInterfacePrefixes = []string{"lo", "veth", "docker", "br-", "virbr", "cni", "flannel"}

// CollectNetwork reports cumulative traffic counters per interface. The
// panel derives rates from successive cycles, so the agent ships the raw
// counters rather than a rate it would have to guess an interval for.
func CollectNetwork() ([]transport.MetricSample, error) {
	lines, err := readLines(ProcNetDev)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, nil
	}

	samples := []transport.MetricSample{}
	for _, stats := range ParseNetDev(lines) {
		labels := map[string]string{"interface": stats.Name}
		samples = append(samples,
			sample("network.rx_bytes", stats.RxBytes, "bytes", labels),
			sample("network.rx_packets", stats.RxPackets, "count", labels),
			sample("network.rx_errors", stats.RxErrors, "count", labels),
			sample("network.tx_bytes", stats.TxBytes, "bytes", labels),
			sample("network.tx_packets", stats.TxPackets, "count", labels),
			sample("network.tx_errors", stats.TxErrors, "count", labels),
		)
	}
	return samples, nil
}

// ParseNetDev turns /proc/net/dev lines into per-interface counters. The
// two header lines and every virtual interface are skipped.
func ParseNetDev(lines []string) []InterfaceStats {
	interfaces := []InterfaceStats{}

	for _, line := range lines {
		name, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}

		name = strings.TrimSpace(name)
		if name == "" || skipInterface(name) {
			continue
		}

		fields := strings.Fields(rest)
		// The columns are: rx bytes packets errs drop fifo frame compressed
		// multicast, then tx bytes packets errs drop fifo colls carrier
		// compressed. Anything shorter is not a counter line.
		if len(fields) < 12 {
			continue
		}

		values := make([]float64, 12)
		valid := true
		for index := 0; index < 12; index++ {
			value, ok := parseFloat(fields[index])
			if !ok {
				valid = false
				break
			}
			values[index] = value
		}
		if !valid {
			continue
		}

		interfaces = append(interfaces, InterfaceStats{
			Name:      name,
			RxBytes:   values[0],
			RxPackets: values[1],
			RxErrors:  values[2],
			TxBytes:   values[8],
			TxPackets: values[9],
			TxErrors:  values[10],
		})
	}
	return interfaces
}

// skipInterface reports whether an interface is virtual and should not be
// charted as node traffic.
func skipInterface(name string) bool {
	for _, prefix := range skippedInterfacePrefixes {
		if name == prefix || strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
