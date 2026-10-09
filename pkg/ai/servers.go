// Package ai runs language models on a node: the weights downloaded and
// checked against their hash, and llama-server serving them as a process of
// the node, with no pod and no container image.
package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tym83/kuberoot/pkg/apis/node"
	"github.com/tym83/kuberoot/pkg/atomicfile"
)

var (
	// StateDir keeps the servers' specs; BlobDir the weights, by hash.
	StateDir = "/var/lib/kuberoot/modelservers"
	BlobDir  = "/var/lib/kuberoot/models"
	RunDir   = "/run/kuberoot/models"
	LogDir   = "/var/log/kuberoot/models"
	Binary   = "/usr/bin/llama-server"
	// CgroupDir holds the servers' memory: all of the node's but Reserve,
	// so that a server asking for more is stopped, and not the node.
	CgroupDir = "/sys/fs/cgroup/kuberoot-models"
	// RestartDelay: a server that exits is started again after this.
	RestartDelay = 30 * time.Second
)

// Servers are the model servers of the node.
type Servers struct {
	// Reserve is the memory kept from the servers for the node itself.
	Reserve int64

	mu       sync.Mutex
	progress map[string]int64 // sha256 -> bytes downloaded so far
}

func (s *Servers) specFile(n string) string { return filepath.Join(StateDir, n+".json") }
func (s *Servers) pidFile(n string) string  { return filepath.Join(RunDir, n+".pid") }

// LogFile is the server's output.
func (s *Servers) LogFile(n string) string { return filepath.Join(LogDir, n+".log") }

// Blob is where weights with this hash are kept.
func Blob(sha string) string { return filepath.Join(BlobDir, sha+".gguf") }

// Save keeps a server's spec; Apply then makes the node match it. The
// weights it served before are remembered, and kept, for a rollback to find
// them still on the node.
func (s *Servers) Save(name string, spec node.ModelServerSpec) error {
	if err := os.MkdirAll(StateDir, 0o700); err != nil {
		return err
	}
	if old, err := s.Load(name); err == nil && old.SHA256 != spec.SHA256 {
		if err := os.WriteFile(s.specFile(name)+".previous", []byte(old.SHA256), 0o600); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(s.specFile(name), raw, 0o600)
}

func (s *Servers) Load(name string) (node.ModelServerSpec, error) {
	var spec node.ModelServerSpec
	raw, err := os.ReadFile(s.specFile(name))
	if err != nil {
		return spec, err
	}
	return spec, json.Unmarshal(raw, &spec)
}

// Names lists the node's model servers.
func (s *Servers) Names() []string {
	files, _ := filepath.Glob(filepath.Join(StateDir, "*.json"))
	var out []string
	for _, f := range files {
		out = append(out, strings.TrimSuffix(filepath.Base(f), ".json"))
	}
	sort.Strings(out)
	return out
}

// Args are llama-server's arguments for a server.
func Args(spec node.ModelServerSpec) []string {
	args := []string{"--model", Blob(spec.SHA256), "--alias", spec.Model,
		"--host", "0.0.0.0", "--port", strconv.Itoa(int(spec.Port)), "--metrics"}
	if spec.ContextSize > 0 {
		args = append(args, "--ctx-size", strconv.Itoa(int(spec.ContextSize)))
	}
	if spec.Parallel > 0 {
		args = append(args, "--parallel", strconv.Itoa(int(spec.Parallel)))
	}
	if spec.Threads > 0 {
		args = append(args, "--threads", strconv.Itoa(int(spec.Threads)))
	}
	return args
}

func configKey(spec node.ModelServerSpec) string { return strings.Join(Args(spec), " ") }

// Apply downloads the weights the server needs, and runs the server with
// them; a server running with other weights or settings is restarted.
func (s *Servers) Apply(ctx context.Context, name string, spec node.ModelServerSpec) error {
	if err := s.fetch(ctx, spec.URL, spec.SHA256); err != nil {
		return err
	}
	pid := s.pid(name)
	if pid != 0 {
		if s.startedWith(name) == configKey(spec) {
			if ok, _ := healthy(spec.Port); ok {
				// Proven: no longer the first one the kernel stops.
				_ = os.WriteFile(fmt.Sprintf("/proc/%d/oom_score_adj", pid), []byte("0"), 0o644)
			}
			return nil
		}
		if err := s.stop(name, pid); err != nil {
			return err
		}
	} else if s.startedWith(name) == configKey(spec) {
		// It ran with these settings and exited: not again at once.
		if info, err := os.Stat(s.pidFile(name) + ".config"); err == nil && time.Since(info.ModTime()) < RestartDelay {
			return nil
		}
	}
	return s.start(name, spec)
}

// cgroup is the server's own group under the servers' limit; the whole
// server goes when it runs out.
func (s *Servers) cgroup(name string) (*os.File, error) {
	if err := os.MkdirAll(CgroupDir, 0o755); err != nil {
		return nil, err
	}
	if total := memTotal(); total > 0 {
		limit := total - s.Reserve
		if limit < total/4 {
			limit = total / 4
		}
		if err := os.WriteFile(filepath.Join(CgroupDir, "memory.max"), []byte(strconv.FormatInt(limit, 10)), 0o644); err != nil {
			return nil, fmt.Errorf("memory limit: %w", err)
		}
	}
	_ = os.WriteFile(filepath.Join(CgroupDir, "memory.swap.max"), []byte("0"), 0o644)
	_ = os.WriteFile(filepath.Join(CgroupDir, "cgroup.subtree_control"), []byte("+memory +cpu"), 0o644)
	dir := filepath.Join(CgroupDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	_ = os.WriteFile(filepath.Join(dir, "memory.oom.group"), []byte("1"), 0o644)
	return os.Open(dir)
}

func memTotal() int64 {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			return kb << 10
		}
	}
	return 0
}

func (s *Servers) start(name string, spec node.ModelServerSpec) error {
	for _, d := range []string{RunDir, LogDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	log, err := os.OpenFile(s.LogFile(name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cg, err := s.cgroup(name)
	if err != nil {
		return err
	}
	defer cg.Close()
	cmd := exec.Command(Binary, Args(spec)...)
	cmd.Stdout, cmd.Stderr = log, log
	// A session of its own: the server outlives the node API. It starts in
	// its cgroup, under the limit from its first instruction.
	cmd.SysProcAttr = procAttr(cg)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start llama-server: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	// Until it serves, a new server is the first the kernel stops when the
	// servers run out of memory, not one that serves already.
	_ = os.WriteFile(fmt.Sprintf("/proc/%d/oom_score_adj", cmd.Process.Pid), []byte("500"), 0o644)
	if err := os.WriteFile(s.pidFile(name)+".config", []byte(configKey(spec)), 0o600); err != nil {
		return err
	}
	return os.WriteFile(s.pidFile(name), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
}

func (s *Servers) startedWith(name string) string {
	raw, _ := os.ReadFile(s.pidFile(name) + ".config")
	return string(raw)
}

func (s *Servers) stop(name string, pid int) error {
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for i := 0; i < 20 && s.pid(name) != 0; i++ {
		time.Sleep(500 * time.Millisecond)
	}
	if s.pid(name) != 0 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		for i := 0; i < 10 && s.pid(name) != 0; i++ {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if s.pid(name) != 0 {
		return fmt.Errorf("the server %s does not exit", name)
	}
	_ = os.Remove(s.pidFile(name))
	return nil
}

// pid is the server's process when it runs, 0 otherwise.
func (s *Servers) pid(name string) int {
	raw, err := os.ReadFile(s.pidFile(name))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || !strings.Contains(string(cmdline), Binary) {
		return 0
	}
	if stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil && strings.Contains(string(stat), ") Z ") {
		return 0
	}
	return pid
}

// fetch downloads weights once, into a file named by their hash; a
// download that does not match the hash is thrown away.
func (s *Servers) fetch(ctx context.Context, url, sha string) error {
	if len(sha) != 64 {
		return fmt.Errorf("sha256 %q: 64 hex digits", sha)
	}
	if _, err := os.Stat(Blob(sha)); err == nil {
		return nil
	}
	if err := os.MkdirAll(BlobDir, 0o700); err != nil {
		return err
	}
	part := Blob(sha) + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	defer os.Remove(part)
	defer f.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	h := sha256.New()
	w := &counter{w: io.MultiWriter(f, h), add: func(n int) { s.addProgress(sha, n) }}
	defer s.clearProgress(sha)
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sha {
		return fmt.Errorf("download %s: sha256 %s, not the %s asked for", url, got, sha)
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return os.Rename(part, Blob(sha))
}

type counter struct {
	w   io.Writer
	add func(int)
}

func (c *counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.add(n)
	return n, err
}

func (s *Servers) addProgress(sha string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.progress == nil {
		s.progress = map[string]int64{}
	}
	s.progress[sha] += int64(n)
}

func (s *Servers) clearProgress(sha string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.progress, sha)
}

func (s *Servers) downloading(sha string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.progress[sha]
	return n, ok
}

// healthy asks the server itself: it answers 200 once the model is loaded.
var healthy = func(port int32) (bool, error) {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// Status tells where the server is: its weights downloading, the model
// loading, the server ready, or not running.
func (s *Servers) Status(name string, spec node.ModelServerSpec) node.ModelServerStatus {
	if n, ok := s.downloading(spec.SHA256); ok {
		return node.ModelServerStatus{Phase: "Downloading", Downloaded: n}
	}
	pid := s.pid(name)
	if pid == 0 {
		st := node.ModelServerStatus{Phase: "Starting", Message: lastLine(s.LogFile(name))}
		if s.startedWith(name) == configKey(spec) {
			// Started with these weights and settings, and gone: they do
			// not run here. The next pass tries again.
			st.Phase = "Failed"
		}
		return st
	}
	st := node.ModelServerStatus{Phase: "Loading", PID: int32(pid)}
	if f := strings.Fields(s.startedWith(name)); len(f) > 1 {
		st.SHA256 = strings.TrimSuffix(filepath.Base(f[1]), ".gguf")
	}
	if info, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
		t := metav1.NewTime(info.ModTime())
		st.StartedAt = &t
	}
	if ok, _ := healthy(spec.Port); ok && st.SHA256 == spec.SHA256 {
		st.Phase = "Ready"
	}
	return st
}

// Delete stops a server and forgets it, and the weights no server needs.
func (s *Servers) Delete(name string) error {
	if pid := s.pid(name); pid != 0 {
		if err := s.stop(name, pid); err != nil {
			return err
		}
	}
	for _, f := range []string{s.specFile(name), s.specFile(name) + ".previous", s.pidFile(name) + ".config"} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return s.Prune()
}

// Prune removes weights that no server runs or ran last.
func (s *Servers) Prune() error {
	keep := map[string]bool{}
	for _, n := range s.Names() {
		if spec, err := s.Load(n); err == nil {
			keep[spec.SHA256] = true
		}
		if prev, err := os.ReadFile(s.specFile(n) + ".previous"); err == nil {
			keep[strings.TrimSpace(string(prev))] = true
		}
	}
	blobs, _ := filepath.Glob(filepath.Join(BlobDir, "*.gguf"))
	for _, b := range blobs {
		if !keep[strings.TrimSuffix(filepath.Base(b), ".gguf")] {
			if err := os.Remove(b); err != nil {
				return err
			}
		}
	}
	return nil
}

func lastLine(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	return lines[len(lines)-1]
}
