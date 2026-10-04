package main

import (
	"fmt"
	"os"
	"strings"
)

var sysctls = map[string]string{
	"net.ipv4.ip_forward":                 "1",
	"net.ipv6.conf.all.forwarding":        "1",
	"net.bridge.bridge-nf-call-iptables":  "1",
	"net.bridge.bridge-nf-call-ip6tables": "1",
	"fs.inotify.max_user_instances":       "8192",
	"fs.inotify.max_user_watches":         "524288",
}

func applySysctls() error {
	for key, value := range sysctls {
		path := "/proc/sys/" + strings.ReplaceAll(key, ".", "/")
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			return fmt.Errorf("sysctl %s: %w", key, err)
		}
	}
	return nil
}
