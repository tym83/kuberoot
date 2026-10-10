package main

import (
	"fmt"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestWithDistroKubelet(t *testing.T) {
	base := fmt.Sprintf(kubeletConfig, "/ca.crt", "10.96.0.10", "/srv.crt", "/srv.key")
	if got, err := withDistroKubelet(base, nil); err != nil || got != base {
		t.Fatalf("no fields changed the configuration: %v", err)
	}
	got, err := withDistroKubelet(base, map[string]any{"cpuManagerPolicy": "static", "reservedSystemCPUs": "0-1"})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal([]byte(got), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["cpuManagerPolicy"] != "static" || cfg["reservedSystemCPUs"] != "0-1" || cfg["tlsCertFile"] != "/srv.crt" ||
		cfg["kind"] != "KubeletConfiguration" {
		t.Fatalf("merged configuration: %s", got)
	}
	if _, err := withDistroKubelet(base, map[string]any{"authentication": map[string]any{}}); err == nil ||
		!strings.Contains(err.Error(), "authentication") {
		t.Fatalf("a field the image manages was replaced: %v", err)
	}
}

func TestRTProfileKubelet(t *testing.T) {
	p, err := loadProfileFile("../../distros/rt/profile.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Kubelet["cpuManagerPolicy"] != "static" {
		t.Fatalf("the rt kubelet hands out CPUs by %v", p.Spec.Kubelet["cpuManagerPolicy"])
	}
	base := fmt.Sprintf(kubeletConfig, "/ca.crt", "10.96.0.10", "/srv.crt", "/srv.key")
	if _, err := withDistroKubelet(base, p.Spec.Kubelet); err != nil {
		t.Fatal(err)
	}
}
