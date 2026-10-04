// Command kinit is PID 1 of every kuberoot distro: it prepares the kernel
// interfaces a Kubernetes node needs and supervises the node services.
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type mount struct {
	source, target, fstype string
	flags                  uintptr
	data                   string
}

var mounts = []mount{
	{"proc", "/proc", "proc", unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC, ""},
	{"sysfs", "/sys", "sysfs", unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC, ""},
	{"devtmpfs", "/dev", "devtmpfs", unix.MS_NOSUID, "mode=0755"},
	{"devpts", "/dev/pts", "devpts", unix.MS_NOSUID | unix.MS_NOEXEC, "gid=5,mode=0620,ptmxmode=0666"},
	{"tmpfs", "/dev/shm", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV, "mode=1777"},
	{"tmpfs", "/run", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV, "mode=0755"},
	{"tmpfs", "/tmp", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV, "mode=1777"},
	{"cgroup2", "/sys/fs/cgroup", "cgroup2", unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC, "nsdelegate"},
	{"bpf", "/sys/fs/bpf", "bpf", unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC, "mode=0700"},
}

func mountAll() error {
	for _, m := range mounts {
		if err := os.MkdirAll(m.target, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", m.target, err)
		}
		err := unix.Mount(m.source, m.target, m.fstype, m.flags, m.data)
		if err != nil && err != unix.EBUSY {
			return fmt.Errorf("mount %s: %w", m.target, err)
		}
	}
	return nil
}

func uptime() time.Duration {
	var info unix.Sysinfo_t
	if unix.Sysinfo(&info) != nil {
		return 0
	}
	return time.Duration(info.Uptime) * time.Second
}

// reap collects orphaned children; as PID 1 nobody else will.
func reap() {
	for {
		var ws unix.WaitStatus
		pid, err := unix.Wait4(-1, &ws, unix.WNOHANG, nil)
		if pid <= 0 || err != nil {
			return
		}
	}
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("[kinit] ")
	if os.Getpid() != 1 {
		log.Fatal("must run as PID 1")
	}
	if err := mountAll(); err != nil {
		log.Fatalf("mount: %v", err)
	}
	var uts unix.Utsname
	_ = unix.Uname(&uts)
	log.Printf("kuberoot booting, kernel %s, uptime %s", unix.ByteSliceToString(uts.Release[:]), uptime())

	if err := setupNetwork(); err != nil {
		log.Printf("network: %v", err)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, unix.SIGCHLD, unix.SIGTERM, unix.SIGINT, unix.SIGPWR)
	log.Printf("ready")
	for sig := range sigs {
		switch sig {
		case unix.SIGCHLD:
			reap()
		default:
			shutdown(sig.(syscall.Signal))
		}
	}
}

func shutdown(sig syscall.Signal) {
	log.Printf("received %s, powering off", sig)
	unix.Sync()
	cmd := unix.LINUX_REBOOT_CMD_POWER_OFF
	if sig == unix.SIGINT {
		cmd = unix.LINUX_REBOOT_CMD_RESTART
	}
	_ = unix.Reboot(cmd)
}
