package gpu

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func fakeProc(t *testing.T) {
	d := t.TempDir()
	ProcGPUs, ProcDevices, Dev, LibDir = d+"/gpus", d+"/devices", d+"/dev", d+"/lib"
	for _, g := range []struct{ bus, info string }{
		{"0000:01:00.0", "Model: \t\t NVIDIA RTX 4000 SFF Ada Generation\nIRQ: 140\nGPU UUID: \t GPU-aaaa\nDevice Minor: \t 0\n"},
		{"0000:02:00.0", "Model: \t\t NVIDIA L4\nGPU UUID: \t GPU-bbbb\nDevice Minor: \t 1\n"},
	} {
		if err := os.MkdirAll(filepath.Join(ProcGPUs, g.bus), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ProcGPUs, g.bus, "information"), []byte(g.info), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	devices := "Character devices:\n  1 mem\n195 nvidia-frontend\n234 nvidia-uvm\n\nBlock devices:\n  8 sd\n"
	if err := os.WriteFile(ProcDevices, []byte(devices), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"libcuda.so.580.178.04", "libcuda.so.1", "libnvidia-ml.so.1"} {
		_ = os.MkdirAll(LibDir, 0o755)
		if err := os.WriteFile(filepath.Join(LibDir, l), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFind(t *testing.T) {
	fakeProc(t)
	gpus, err := Find()
	if err != nil || len(gpus) != 2 {
		t.Fatalf("%v %v", gpus, err)
	}
	if gpus[0].Model != "NVIDIA RTX 4000 SFF Ada Generation" || gpus[0].UUID != "GPU-aaaa" || gpus[1].Minor != 1 {
		t.Fatalf("parsed %+v", gpus)
	}
	ProcGPUs = "/nonexistent"
	if gpus, err := Find(); err != nil || gpus != nil {
		t.Fatalf("no driver: %v %v", gpus, err)
	}
}

func TestNodes(t *testing.T) {
	fakeProc(t)
	gpus, _ := Find()
	m, err := majors()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range nodes(gpus, m) {
		got = append(got, strings.TrimPrefix(n.path, Dev+"/")+":"+itoa(n.major)+","+itoa(n.minor))
	}
	want := "nvidiactl:195,255 nvidia0:195,0 nvidia1:195,1 nvidia-uvm:234,0 nvidia-uvm-tools:234,1"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %s\nwant %s", strings.Join(got, " "), want)
	}
}

func itoa(n uint32) string { return strconv.FormatUint(uint64(n), 10) }

func TestCDI(t *testing.T) {
	fakeProc(t)
	gpus, _ := Find()
	devs := []string{Dev + "/nvidiactl", Dev + "/nvidia0", Dev + "/nvidia1", Dev + "/nvidia-uvm"}
	raw, err := CDI(gpus, devs)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"kind: nvidia.com/gpu", "name: \"0\"", "name: GPU-bbbb", "name: all",
		"path: " + Dev + "/nvidiactl", "path: " + Dev + "/nvidia-uvm", "hostPath: " + LibDir + "/libcuda.so.1"} {
		if !strings.Contains(s, want) {
			t.Errorf("spec lacks %q:\n%s", want, s)
		}
	}
	// A GPU's own device file is only in its device, not in every container.
	common := s[strings.Index(s, "containerEdits:\n  deviceNodes"):]
	if strings.Contains(common[:strings.Index(common, "mounts")], "/nvidia0") {
		t.Errorf("a GPU's device file is shared by all:\n%s", s)
	}
}
