package main

import (
	"net"
	"testing"
)

func TestClusterNetDefaultsAndDerivedAddresses(t *testing.T) {
	cn, err := parseClusterNet("", "")
	if err != nil {
		t.Fatal(err)
	}
	if cn.pod.String() != defaultPodCIDR || cn.service.String() != defaultServiceCIDR {
		t.Errorf("ranges = %s", cn)
	}
	if got := cn.apiServiceIP().String(); got != "10.201.0.1" {
		t.Errorf("API service IP = %s", got)
	}
	if got := cn.dnsIP().String(); got != "10.201.0.10" {
		t.Errorf("DNS IP = %s", got)
	}
}

func TestNodeSubnets(t *testing.T) {
	cn, _ := parseClusterNet("10.50.0.0/16", "10.51.0.0/16")
	node := nodeInfo{ip: net.ParseIP("192.168.100.12")}
	if got := cn.nodeSubnet(node, true); got != "10.50.0.0/24" {
		t.Errorf("control plane subnet = %s", got)
	}
	if got := cn.nodeSubnet(node, false); got != "10.50.12.0/24" {
		t.Errorf("worker subnet = %s", got)
	}
	// A worker whose last octet is 0 must not take the control plane's subnet.
	zero := nodeInfo{ip: net.ParseIP("10.0.1.0")}
	if got := cn.nodeSubnet(zero, false); got == "10.50.0.0/24" {
		t.Errorf("worker with octet 0 got the control plane subnet")
	}
}

func TestClusterNetRejects(t *testing.T) {
	for _, c := range [][2]string{{"10.0.0.0/24", ""}, {"nonsense", ""}, {"", "10.0.0.0/33"}} {
		if _, err := parseClusterNet(c[0], c[1]); err == nil {
			t.Errorf("parseClusterNet(%q, %q) accepted", c[0], c[1])
		}
	}
}

func TestParseCmdline(t *testing.T) {
	cfg := parseCmdline("console=ttyS0 kuberoot.dev kuberoot.mode=install kuberoot.nameserver=1.1.1.1,8.8.8.8 " +
		"kuberoot.ip=eth1:10.0.0.5/24 kuberoot.distro=router kuberoot.repo-plain-http kuberoot.pod-cidr=10.50.0.0/16")
	if !cfg.dev || !cfg.install || !cfg.repoPlainHTTP {
		t.Errorf("switches = %+v", cfg)
	}
	if len(cfg.nameservers) != 2 || cfg.static != "eth1:10.0.0.5/24" || cfg.distro != "router" || cfg.podCIDR != "10.50.0.0/16" {
		t.Errorf("values = %+v", cfg)
	}
	if def := parseCmdline(""); def.distro != "edge" || def.repo == "" {
		t.Errorf("defaults = %+v", def)
	}
}
