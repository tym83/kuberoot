package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type mount struct {
	source, target, fstype string
	flags                  uintptr
	data                   string
}

// Stage two mounts; whatever stage one already moved into place is skipped.
var mounts = []mount{
	{"proc", "/proc", "proc", unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC, ""},
	{"sysfs", "/sys", "sysfs", unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC, ""},
	{"devtmpfs", "/dev", "devtmpfs", unix.MS_NOSUID, "mode=0755"},
	{"devpts", "/dev/pts", "devpts", unix.MS_NOSUID | unix.MS_NOEXEC, "gid=5,mode=0620,ptmxmode=0666"},
	{"tmpfs", "/dev/shm", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV, "mode=1777"},
	{"tmpfs", "/run", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV, "mode=0755"},
	{"tmpfs", "/tmp", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV, "mode=1777"},
	{"tmpfs", "/var", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV, "mode=0755"},
	{"cgroup2", "/sys/fs/cgroup", "cgroup2", unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC, "nsdelegate"},
	{"bpf", "/sys/fs/bpf", "bpf", unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC, "mode=0700"},
}

// cniPluginDir holds CNI plugins that packages install; it appears at
// /opt/cni/bin, where they expect it.
const cniPluginDir = "/var/lib/cni/bin"

func mountAll() error {
	for _, m := range mounts {
		if err := os.MkdirAll(m.target, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", m.target, err)
		}
		if isMountpoint(m.target) {
			continue
		}
		if err := unix.Mount(m.source, m.target, m.fstype, m.flags, m.data); err != nil {
			return fmt.Errorf("mount %s: %w", m.target, err)
		}
	}
	// CNI packages install their plugins into /opt/cni/bin by convention;
	// the root is read-only, so that directory is a writable one from /var.
	if err := os.MkdirAll(cniPluginDir, 0o755); err == nil && !isMountpoint("/opt/cni/bin") {
		if err := unix.Mount(cniPluginDir, "/opt/cni/bin", "", unix.MS_BIND, ""); err != nil {
			return fmt.Errorf("mount /opt/cni/bin: %w", err)
		}
	}
	// Mounts propagate both ways, as under systemd: the kubelet and the
	// runtime need shared mounts for Bidirectional volume mounts (CSI
	// drivers, virt-handler), and refuse them below a private mount.
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_SHARED, ""); err != nil {
		return fmt.Errorf("make mounts shared: %w", err)
	}
	return nil
}

// isMountpoint reports whether dir sits on a different device than its parent.
func isMountpoint(dir string) bool {
	var self, parent unix.Stat_t
	if unix.Stat(dir, &self) != nil || unix.Stat(filepath.Dir(dir), &parent) != nil {
		return false
	}
	return self.Dev != parent.Dev
}

func unixSeconds(s int64) time.Duration { return time.Duration(s) * time.Second }
