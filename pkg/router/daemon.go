package router

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// Daemon runs a program the router needs (dnsmasq, bird) and starts it again
// when it exits, until it is stopped.
type Daemon struct {
	Name string
	Args []string

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	running bool
}

// Start runs the program unless it already runs.
func (d *Daemon) Start() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.running {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel, d.done, d.running = cancel, make(chan struct{}), true
	go d.loop(ctx, d.done)
}

// Stop ends the program and waits for it.
func (d *Daemon) Stop() {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return
	}
	cancel, done := d.cancel, d.done
	d.running = false
	d.mu.Unlock()
	cancel()
	<-done
}

// Restart stops the program and runs it again, as after a configuration
// change it does not reload on its own.
func (d *Daemon) Restart() {
	d.Stop()
	d.Start()
}

// Running reports whether the program is meant to run.
func (d *Daemon) Running() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running
}

func (d *Daemon) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	backoff := time.Second
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, d.Args[0], d.Args[1:]...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
		cmd.WaitDelay = 10 * time.Second
		start := time.Now()
		err := cmd.Run()
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		klog.Errorf("%s exited (%v), starting again in %s", d.Name, err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}
