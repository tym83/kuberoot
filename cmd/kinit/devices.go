package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// findPartition waits for a block device whose GPT partition name matches label.
// Disks behind USB or slow controllers can take several seconds to appear.
func findPartition(label string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if dev := partitionByLabel(label); dev != "" {
			return dev, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("partition %q not found", label)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func partitionByLabel(label string) string {
	uevents, _ := filepath.Glob("/sys/class/block/*/uevent")
	for _, path := range uevents {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var name, dev string
		for _, line := range strings.Split(string(raw), "\n") {
			if v, ok := strings.CutPrefix(line, "PARTNAME="); ok {
				name = v
			}
			if v, ok := strings.CutPrefix(line, "DEVNAME="); ok {
				dev = v
			}
		}
		if name == label && dev != "" {
			return "/dev/" + dev
		}
	}
	return ""
}

// cmdlineValue returns the value of key=value on the kernel command line.
func cmdlineValue(key string) string {
	raw, _ := os.ReadFile("/proc/cmdline")
	for _, f := range strings.Fields(string(raw)) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	return ""
}
