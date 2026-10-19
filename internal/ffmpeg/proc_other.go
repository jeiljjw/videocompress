//go:build !windows

package ffmpeg

import "syscall"

func hiddenProcAttr() *syscall.SysProcAttr { return nil }
