//go:build linux

package metrics

import "syscall"

// diskUsage returns the used and total bytes of the filesystem holding path.
// Used is computed from the blocks unavailable to an unprivileged process,
// which is what a disk-full alert cares about.
func diskUsage(path string) (used, total uint64, ok bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, false
	}

	blockSize := uint64(stat.Bsize)
	if blockSize == 0 {
		return 0, 0, false
	}

	total = uint64(stat.Blocks) * blockSize
	free := uint64(stat.Bavail) * blockSize

	if free > total {
		return 0, 0, false
	}

	return total - free, total, true
}
