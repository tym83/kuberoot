//go:build !linux

package ai

import (
	"os"
	"syscall"
)

// procAttr: elsewhere than Linux, a session of its own and no cgroup.
func procAttr(*os.File) *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
