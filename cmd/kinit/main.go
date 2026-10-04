// Command kinit is PID 1 of every kuberoot distro.
//
// Stage one runs from the initramfs: it mounts the read-only root filesystem
// image and switches into it. Stage two runs from that root: it prepares the
// kernel interfaces a Kubernetes node needs and supervises the node services.
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

const rootImage = "/rootfs.squashfs"

func main() {
	log.SetFlags(0)
	log.SetPrefix("[kinit] ")
	if os.Getpid() != 1 {
		log.Fatal("must run as PID 1")
	}
	if isInitramfs() {
		if err := switchRoot(); err != nil {
			log.Fatalf("switch root: %v", err)
		}
		return // unreachable: switchRoot execs stage two
	}
	if err := boot(); err != nil {
		log.Printf("boot failed: %v", err)
	}
	waitForSignals()
}

// isInitramfs tells stage one from stage two: only the initramfs has /newroot.
func isInitramfs() bool {
	_, err := os.Stat(newRoot)
	return err == nil
}

func boot() error {
	if err := mountAll(); err != nil {
		return err
	}
	var uts unix.Utsname
	_ = unix.Uname(&uts)
	log.Printf("kuberoot booting, kernel %s, uptime %s", unix.ByteSliceToString(uts.Release[:]), uptime())

	waitForEntropy()
	cfg := loadCmdline()
	if err := applySysctls(); err != nil {
		return err
	}
	node, err := setupNetwork(cfg)
	if err != nil {
		return err
	}
	if err := setHostname(node); err != nil {
		return err
	}
	services, controlPlane, err := configure(node)
	if err != nil {
		return err
	}
	if cfg.dev {
		printNodeKubeconfig()
	}
	serveControl(func() { reconfigure(node, cfg) })
	startServices(services, cfg)
	if controlPlane {
		go applyAddons(cfg, node)
	}
	return nil
}

// waitForEntropy says so on the console when the kernel random pool is not ready:
// key generation would block silently on hardware without an RNG source.
func waitForEntropy() {
	buf := make([]byte, 1)
	if _, err := unix.Getrandom(buf, unix.GRND_NONBLOCK); err != unix.EAGAIN {
		return
	}
	log.Printf("waiting for the kernel random pool (no hardware RNG?)")
	_, _ = unix.Getrandom(buf, 0)
	log.Printf("random pool ready, uptime %s", uptime())
}

func waitForSignals() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, unix.SIGCHLD, unix.SIGTERM, unix.SIGINT, unix.SIGPWR)
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
	log.Printf("received %s, stopping services", sig)
	stopServices()
	unix.Sync()
	// The state partition is ext4 on an installed system; leave it clean.
	_ = unix.Unmount("/var", unix.MNT_DETACH)
	unix.Sync()
	cmd := unix.LINUX_REBOOT_CMD_POWER_OFF
	if sig == unix.SIGINT {
		cmd = unix.LINUX_REBOOT_CMD_RESTART
	}
	_ = unix.Reboot(cmd)
}

func uptime() string {
	var info unix.Sysinfo_t
	if unix.Sysinfo(&info) != nil {
		return "?"
	}
	return (unixSeconds(info.Uptime)).String()
}
