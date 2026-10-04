package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

const (
	kubeDir     = "/etc/kubernetes"
	serviceCIDR = "10.96.0.0/12"
	clusterCIDR = "10.244.0.0/16"
	podCIDR     = "10.244.0.0/24"
	clusterDNS  = "10.96.0.10"
	apiServer   = "https://127.0.0.1:6443"

	nodeAPIServer       = "https://127.0.0.1:50000"
	nodeAdminKubeconfig = "/etc/kuberoot/node-admin.kubeconfig"
)

// writeNodeConfig generates the PKI and every config file the node services read.
func writeNodeConfig(node nodeInfo) error {
	if err := createPKI(node); err != nil {
		return fmt.Errorf("pki: %w", err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	files := map[string]string{
		"/etc/machine-id":                      hex.EncodeToString(id) + "\n",
		"/etc/containerd/config.toml":          containerdConfig,
		"/etc/cni/net.d/10-kuberoot.conflist":  fmt.Sprintf(cniConfig, podCIDR),
		filepath.Join(kubeDir, "kubelet.yaml"): fmt.Sprintf(kubeletConfig, pkiPath("ca.crt"), clusterDNS, pkiPath("kubelet-server.crt"), pkiPath("kubelet-server.key")),
	}
	for _, user := range []string{"admin", "controller-manager", "scheduler", "kube-proxy", "kubelet-client"} {
		kc, err := kubeconfig(apiServer, "ca.crt", "kuberoot", user)
		if err != nil {
			return err
		}
		files[filepath.Join(kubeDir, user+".kubeconfig")] = kc
	}
	nodeAdmin, err := kubeconfig(nodeAPIServer, "node-ca.crt", node.name, "node-admin")
	if err != nil {
		return err
	}
	files[nodeAdminKubeconfig] = nodeAdmin
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
	}
	for _, dir := range []string{"/var/lib/kine", "/var/lib/kubelet", "/var/lib/containerd", "/var/log/kuberoot"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// kubeconfig embeds the credentials so the file is usable off the node too.
func kubeconfig(server, caFile, name, user string) (string, error) {
	b64 := func(name string) (string, error) {
		raw, err := os.ReadFile(pkiPath(name))
		return base64.StdEncoding.EncodeToString(raw), err
	}
	ca, err := b64(caFile)
	if err != nil {
		return "", err
	}
	crt, err := b64(user + ".crt")
	if err != nil {
		return "", err
	}
	key, err := b64(user + ".key")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: %[1]s
  cluster:
    server: %[2]s
    certificate-authority-data: %[3]s
users:
- name: %[1]s-%[4]s
  user:
    client-certificate-data: %[5]s
    client-key-data: %[6]s
contexts:
- name: %[1]s
  context:
    cluster: %[1]s
    user: %[1]s-%[4]s
current-context: %[1]s
`, name, server, ca, user, crt, key), nil
}

const containerdConfig = `version = 3
root = "/var/lib/containerd"
state = "/run/containerd"

[grpc]
  address = "/run/containerd/containerd.sock"

[plugins.'io.containerd.cri.v1.runtime'.cni]
  bin_dirs = ["/usr/libexec/cni"]
  conf_dir = "/etc/cni/net.d"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc.options]
  BinaryName = "/usr/bin/runc"
  SystemdCgroup = false
`

const cniConfig = `{
  "cniVersion": "1.0.0",
  "name": "kuberoot",
  "plugins": [
    {
      "type": "bridge",
      "bridge": "cni0",
      "isGateway": true,
      "ipMasq": true,
      "ipMasqBackend": "nftables",
      "hairpinMode": true,
      "ipam": {
        "type": "host-local",
        "ranges": [[{"subnet": "%s"}]],
        "routes": [{"dst": "0.0.0.0/0"}]
      }
    },
    {"type": "portmap", "backend": "nftables", "capabilities": {"portMappings": true}}
  ]
}
`

const kubeletConfig = `apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
authentication:
  anonymous:
    enabled: false
  webhook:
    enabled: true
  x509:
    clientCAFile: %s
authorization:
  mode: Webhook
cgroupDriver: cgroupfs
containerRuntimeEndpoint: unix:///run/containerd/containerd.sock
clusterDomain: cluster.local
clusterDNS:
- %s
tlsCertFile: %s
tlsPrivateKeyFile: %s
failSwapOn: false
resolvConf: /etc/resolv.conf
`
