//go:build windows

package collector

import (
	"strings"
	"syscall"
	"unsafe"
)

// kernel32 exposes the volume space APIs. Loading it lazily keeps the
// agent a pure-Go binary with no cgo.
var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceExW = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGetLogicalDrives    = kernel32.NewProc("GetLogicalDrives")
)

// DefaultMountPoints lists every drive letter currently present, which is
// the Windows equivalent of the mount table the agent reads on Linux.
func DefaultMountPoints() []string {
	mask, _, _ := procGetLogicalDrives.Call()
	if mask == 0 {
		return []string{`C:\`}
	}

	drives := []string{}
	for index := 0; index < 26; index++ {
		if mask&(1<<uint(index)) == 0 {
			continue
		}
		drives = append(drives, string(rune('A'+index))+`:\`)
	}
	return drives
}

// diskUsage measures one volume with GetDiskFreeSpaceEx. A volume that
// cannot be queried (no media in the drive, access denied) is reported as
// unavailable rather than as an error.
func diskUsage(mount string) (Usage, bool) {
	path := mount
	if !strings.HasSuffix(path, `\`) {
		path += `\`
	}

	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return Usage{}, false
	}

	var freeToCaller, total, free uint64
	ret, _, _ := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&free)),
	)
	if ret == 0 || total == 0 {
		return Usage{}, false
	}

	return Usage{
		MountPoint: mount,
		Total:      float64(total),
		Free:       float64(freeToCaller),
	}, true
}
