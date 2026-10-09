package router

import (
	"strings"
	"testing"
	"time"
)

func TestDaemonReportsWhyItKeepsExiting(t *testing.T) {
	d := &Daemon{Name: "broken", Args: []string{"/bin/sh", "-c", "echo 'unknown user or group: root' >&2; exit 1"}}
	d.Start()
	defer d.Stop()
	for i := 0; i < 50 && d.Failure() == ""; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if f := d.Failure(); !strings.Contains(f, "broken exited: unknown user or group: root") {
		t.Errorf("Failure() = %q", f)
	}
}

func TestDaemonRunningHasNoFailure(t *testing.T) {
	d := &Daemon{Name: "sleeper", Args: []string{"/bin/sh", "-c", "exec sleep 30"}}
	d.Start()
	time.Sleep(300 * time.Millisecond)
	if f := d.Failure(); f != "" {
		t.Errorf("a running program reports %q", f)
	}
	d.Stop()
	if d.Running() {
		t.Error("still running after Stop")
	}
}
