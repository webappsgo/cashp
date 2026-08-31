//go:build !windows

package collector

import "syscall"

// DefaultMountPoints is what the agent measures when no mount table is
// available: the root filesystem.
func DefaultMountPoints() []string {
	return []string{"/"}
}

// diskUsage measures one mount point with statfs. A mount that cannot be
// queried (unmounted between the read and the call, or permission denied)
// is reported as unavailable rather than as an error.
func diskUsage(mount string) (Usage, bool) {
	stat := syscall.Statfs_t{}
	if err := syscall.Statfs(mount, &stat); err != nil {
		return Usage{}, false
	}

	blockSize := float64(stat.Bsize)
	if blockSize <= 0 {
		return Usage{}, false
	}

	return Usage{
		MountPoint: mount,
		Total:      float64(stat.Blocks) * blockSize,
		Free:       float64(stat.Bavail) * blockSize,
	}, true
}
