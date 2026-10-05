package main

import (
	"os"
	"strings"
)

// bootConfig holds the kuberoot.* kernel parameters.
type bootConfig struct {
	// dev prints the admin kubeconfig to the console; development builds only.
	dev bool
	// verbose streams every service's output to the console.
	verbose bool
	// nameservers override the DNS servers handed out by DHCP.
	nameservers []string
	// install runs the console installer: the system booted from boot media.
	install bool
	// distro names the kuberoot distribution; its add-ons are the kubepkg
	// meta package kuberoot-<distro>.
	distro string
	// repo is the kubepkg repository index, repoKey its public key (base64 PEM),
	// and repoPlainHTTP allows a test registry without TLS.
	repo, repoKey string
	repoPlainHTTP bool
	// podCIDR and serviceCIDR set the address ranges of a cluster this node runs.
	podCIDR, serviceCIDR string
	// static is an interface with a fixed address, "eth1:192.168.100.11/24";
	// that address becomes the node address in the cluster.
	static string
}

func loadCmdline() bootConfig {
	raw, _ := os.ReadFile("/proc/cmdline")
	var cfg bootConfig
	for _, f := range strings.Fields(string(raw)) {
		switch f {
		case "kuberoot.dev":
			cfg.dev = true
		case "kuberoot.verbose":
			cfg.verbose = true
		case "kuberoot.mode=install":
			cfg.install = true
		case "kuberoot.repo-plain-http":
			cfg.repoPlainHTTP = true
		default:
			if v, ok := strings.CutPrefix(f, "kuberoot.nameserver="); ok {
				cfg.nameservers = append(cfg.nameservers, strings.Split(v, ",")...)
			}
			if v, ok := strings.CutPrefix(f, "kuberoot.ip="); ok {
				cfg.static = v
			}
			if v, ok := strings.CutPrefix(f, "kuberoot.distro="); ok {
				cfg.distro = v
			}
			if v, ok := strings.CutPrefix(f, "kuberoot.repo="); ok {
				cfg.repo = v
			}
			if v, ok := strings.CutPrefix(f, "kuberoot.repo-key="); ok {
				cfg.repoKey = v
			}
			if v, ok := strings.CutPrefix(f, "kuberoot.pod-cidr="); ok {
				cfg.podCIDR = v
			}
			if v, ok := strings.CutPrefix(f, "kuberoot.service-cidr="); ok {
				cfg.serviceCIDR = v
			}
		}
	}
	if cfg.distro == "" {
		cfg.distro = "edge"
	}
	if cfg.repo == "" {
		cfg.repo = "https://tym83.github.io/kubepkg-recipes/index.yaml"
	}
	return cfg
}
