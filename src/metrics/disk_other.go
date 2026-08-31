//go:build !linux

package metrics

// diskUsage reports no result on platforms without the Linux statfs call.
// The system disk metrics are simply absent there rather than wrong.
func diskUsage(_ string) (used, total uint64, ok bool) {
	return 0, 0, false
}
