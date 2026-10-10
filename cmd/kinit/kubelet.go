package main

import (
	"fmt"

	"sigs.k8s.io/yaml"
)

// kubeletManaged are the fields the image sets for the node to work at all:
// a distribution may not set them.
var kubeletManaged = map[string]bool{
	"apiVersion": true, "kind": true, "authentication": true, "authorization": true,
	"cgroupDriver": true, "containerRuntimeEndpoint": true, "clusterDomain": true, "clusterDNS": true,
	"tlsCertFile": true, "tlsPrivateKeyFile": true, "rotateCertificates": true, "serverTLSBootstrap": true,
}

// withDistroKubelet sets the distribution's kubelet fields on the image's
// configuration.
func withDistroKubelet(cfg string, extra map[string]any) (string, error) {
	if len(extra) == 0 {
		return cfg, nil
	}
	var merged map[string]any
	if err := yaml.Unmarshal([]byte(cfg), &merged); err != nil {
		return "", fmt.Errorf("kubelet configuration: %w", err)
	}
	for k, v := range extra {
		if kubeletManaged[k] {
			return "", fmt.Errorf("the distribution sets the kubelet's %s, which the image manages", k)
		}
		merged[k] = v
	}
	out, err := yaml.Marshal(merged)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
