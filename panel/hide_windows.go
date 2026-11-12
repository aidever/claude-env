//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideWindow 避免每次调用 docker 都闪一下黑色命令行窗口。
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
