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
		default:
			if v, ok := strings.CutPrefix(f, "kuberoot.nameserver="); ok {
				cfg.nameservers = append(cfg.nameservers, strings.Split(v, ",")...)
			}
		}
	}
	return cfg
}
