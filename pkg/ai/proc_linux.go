package ai

import (
	"os"
	"syscall"
)

// procAttr starts a server in a session of its own, inside its cgroup.
func procAttr(cg *os.File) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, UseCgroupFD: true, CgroupFD: int(cg.Fd())}
}
