//go:build windows

package ffmpeg

import "syscall"

// hiddenProcAttr avoids flashing console windows when the GUI spawns FFmpeg.
func hiddenProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
