//go:build linux || freebsd || netbsd || openbsd || dragonfly

package terminal

import (
	"syscall"
	"unsafe"
)

// winsize mirrors the kernel struct filled in by the TIOCGWINSZ ioctl.
type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

// termSize asks the kernel for the window size of the given descriptor.
func termSize(fd uintptr) (cols, rows int, ok bool) {
	var ws winsize
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 {
		return 0, 0, false
	}
	return int(ws.Col), int(ws.Row), true
}
