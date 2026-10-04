package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const serviceCgroups = "/sys/fs/cgroup/kuberoot"

var servicePath = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}

type service struct {
	name string
	args []string
}

// children maps the PIDs kinit started to the channel waiting for their exit.
// As PID 1 kinit reaps everything itself, so exec.Cmd.Wait is never used.
var (
	childrenMu sync.Mutex
	children   = map[int]chan unix.WaitStatus{}
	running    sync.Map // service name -> *os.Process
	stopping   bool
)

func nodeServices(node nodeInfo) []service {
	ip := node.ip.String()
	return []service{
		{"containerd", []string{"/usr/bin/containerd", "--config", "/etc/containerd/config.toml"}},
		{"kine", []string{"/usr/bin/kine",
			"--endpoint", "sqlite:///var/lib/kine/state.db?_journal=WAL&cache=shared",
			"--listen-address", "127.0.0.1:2379"}},
		{"kube-apiserver", []string{"/usr/bin/kube-apiserver",
			"--etcd-servers=http://127.0.0.1:2379",
			"--advertise-address=" + ip,
			"--secure-port=6443",
			"--service-cluster-ip-range=" + serviceCIDR,
			"--client-ca-file=" + pkiPath("ca.crt"),
			"--tls-cert-file=" + pkiPath("apiserver.crt"),
			"--tls-private-key-file=" + pkiPath("apiserver.key"),
			"--kubelet-client-certificate=" + pkiPath("apiserver-kubelet-client.crt"),
			"--kubelet-client-key=" + pkiPath("apiserver-kubelet-client.key"),
			"--kubelet-certificate-authority=" + pkiPath("ca.crt"),
			"--kubelet-preferred-address-types=InternalIP",
			"--service-account-issuer=https://kubernetes.default.svc.cluster.local",
			"--service-account-key-file=" + pkiPath("sa.pub"),
			"--service-account-signing-key-file=" + pkiPath("sa.key"),
			"--authorization-mode=Node,RBAC",
			"--enable-admission-plugins=NodeRestriction",
			"--allow-privileged=true",
		}},
		{"kube-controller-manager", []string{"/usr/bin/kube-controller-manager",
			"--kubeconfig=" + kubeDir + "/controller-manager.kubeconfig",
			"--authentication-kubeconfig=" + kubeDir + "/controller-manager.kubeconfig",
			"--authorization-kubeconfig=" + kubeDir + "/controller-manager.kubeconfig",
			"--root-ca-file=" + pkiPath("ca.crt"),
			"--service-account-private-key-file=" + pkiPath("sa.key"),
			"--cluster-signing-cert-file=" + pkiPath("ca.crt"),
			"--cluster-signing-key-file=" + pkiPath("ca.key"),
			"--use-service-account-credentials=true",
			"--leader-elect=false",
		}},
		{"kube-scheduler", []string{"/usr/bin/kube-scheduler",
			"--kubeconfig=" + kubeDir + "/scheduler.kubeconfig",
			"--authentication-kubeconfig=" + kubeDir + "/scheduler.kubeconfig",
			"--authorization-kubeconfig=" + kubeDir + "/scheduler.kubeconfig",
			"--leader-elect=false",
		}},
		{"kubelet", []string{"/usr/bin/kubelet",
			"--config=" + kubeDir + "/kubelet.yaml",
			"--kubeconfig=" + kubeDir + "/kubelet-client.kubeconfig",
			"--hostname-override=" + node.name,
			"--node-ip=" + ip,
		}},
		{"kube-proxy", []string{"/usr/bin/kube-proxy",
			"--kubeconfig=" + kubeDir + "/kube-proxy.kubeconfig",
			"--proxy-mode=nftables",
			"--cluster-cidr=" + clusterCIDR,
			"--hostname-override=" + node.name,
		}},
	}
}

func startServices(node nodeInfo, cfg bootConfig) {
	// Inherited by every service and, through containerd, by containers.
	limit := &unix.Rlimit{Cur: 1 << 20, Max: 1 << 20}
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, limit); err != nil {
		log.Printf("raise open files limit: %v", err)
	}
	controllers := []byte("+cpu +cpuset +memory +pids +io +hugetlb")
	_ = os.WriteFile("/sys/fs/cgroup/cgroup.subtree_control", controllers, 0o644)
	_ = os.MkdirAll(serviceCgroups, 0o755)
	_ = os.WriteFile(filepath.Join(serviceCgroups, "cgroup.subtree_control"), controllers, 0o644)
	for _, s := range nodeServices(node) {
		go supervise(s, cfg.verbose)
	}
}

// supervise keeps a service running, restarting it with backoff when it exits.
func supervise(s service, verbose bool) {
	backoff := time.Second
	for !stopping {
		started := time.Now()
		status, err := runService(s, verbose)
		if stopping {
			return
		}
		if err != nil {
			log.Printf("%s: %v", s.name, err)
		} else {
			log.Printf("%s exited (%s), restarting in %s", s.name, describe(status), backoff)
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		time.Sleep(backoff)
		backoff = min(backoff*2, 30*time.Second)
	}
}

func runService(s service, verbose bool) (unix.WaitStatus, error) {
	logFile, err := os.OpenFile(filepath.Join("/var/log/kuberoot", s.name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer logFile.Close()
	var out io.Writer = logFile
	if verbose {
		out = io.MultiWriter(logFile, &prefixWriter{prefix: "[" + s.name + "] ", w: os.Stdout})
	}
	cmd := exec.Command(s.args[0], s.args[1:]...)
	cmd.Env = servicePath
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &unix.SysProcAttr{Setsid: true}
	done, err := spawn(cmd)
	if err != nil {
		return 0, err
	}
	running.Store(s.name, cmd.Process)
	joinCgroup(s.name, cmd.Process.Pid)
	status := <-done
	running.Delete(s.name)
	return status, nil
}

// spawn starts cmd and returns a channel that receives its exit status from the reaper.
func spawn(cmd *exec.Cmd) (chan unix.WaitStatus, error) {
	childrenMu.Lock()
	defer childrenMu.Unlock()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan unix.WaitStatus, 1)
	children[cmd.Process.Pid] = done
	return done, nil
}

// reap collects every exited child: the ones kinit tracks and orphans reparented to PID 1.
func reap() {
	for {
		var ws unix.WaitStatus
		pid, err := unix.Wait4(-1, &ws, unix.WNOHANG, nil)
		if pid <= 0 || err != nil {
			return
		}
		childrenMu.Lock()
		if done, ok := children[pid]; ok {
			done <- ws
			delete(children, pid)
		}
		childrenMu.Unlock()
	}
}

// joinCgroup moves a service into its own cgroup so its usage is accounted separately.
func joinCgroup(name string, pid int) {
	dir := filepath.Join(serviceCgroups, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0o644)
}

func stopServices() {
	stopping = true
	running.Range(func(_, p any) bool {
		_ = p.(*os.Process).Signal(unix.SIGTERM)
		return true
	})
	time.Sleep(3 * time.Second)
}

func describe(ws unix.WaitStatus) string {
	if ws.Signaled() {
		return "signal " + ws.Signal().String()
	}
	return fmt.Sprintf("code %d", ws.ExitStatus())
}

type prefixWriter struct {
	prefix string
	w      io.Writer
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	_, err := p.w.Write(append([]byte(p.prefix), b...))
	return len(b), err
}
