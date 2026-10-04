package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/vishvananda/netlink"

	nodev1 "github.com/tym83/kuberoot/pkg/apis/node/v1alpha1"
	"github.com/tym83/kuberoot/pkg/membership"
)

// clusterDir keeps what a worker received from its cluster.
const clusterDir = "/var/lib/kuberoot/cluster"

// configure writes the configuration for the node's current role and returns
// the services that role runs. A node without a membership runs its own cluster.
func configure(node nodeInfo) ([]service, bool, error) {
	m, err := membership.Load()
	if err != nil {
		return nil, false, fmt.Errorf("membership: %w", err)
	}
	if m == nil {
		if err := writeNodeConfig(node); err != nil {
			return nil, false, err
		}
		return nodeServices(node), true, nil
	}
	log.Printf("joining %s as %s", m.Server, strings.ToLower(m.Role))
	if err := writeWorkerConfig(node, *m); err != nil {
		return nil, false, err
	}
	return workerServices(node), false, nil
}

// reconfigure switches roles in place: the node joined or left a cluster.
func reconfigure(node nodeInfo, cfg bootConfig) {
	log.Printf("reconfiguring")
	stopServices()
	// The pod bridge keeps the old role's gateway address; the CNI plugin
	// recreates it for the new pod subnet.
	if link, err := netlink.LinkByName("cni0"); err == nil {
		_ = netlink.LinkDel(link)
	}
	services, controlPlane, err := configure(node)
	if err != nil {
		log.Printf("reconfigure: %v", err)
		return
	}
	startServices(services, cfg)
	if controlPlane {
		go applyAddons(cfg, node)
	}
}

func writeWorkerConfig(node nodeInfo, m nodev1.MembershipSpec) error {
	if err := os.MkdirAll(pkiDir, 0o700); err != nil {
		return err
	}
	if err := createNodePKI(node); err != nil {
		return err
	}
	if err := os.MkdirAll(clusterDir, 0o700); err != nil {
		return err
	}
	clusterFiles := map[string]string{
		"ca.crt":             m.ClusterCA,
		"front-proxy-ca.crt": m.FrontProxyCA,
		"node-api.crt":       m.NodeAPICert,
		"node-api.key":       m.NodeAPIKey,
	}
	for name, content := range clusterFiles {
		if err := os.WriteFile(filepath.Join(clusterDir, name), []byte(content), 0o600); err != nil {
			return err
		}
	}
	machineID, err := persistentMachineID()
	if err != nil {
		return err
	}
	ca := base64.StdEncoding.EncodeToString([]byte(m.ClusterCA))
	kubeletPKI := "/var/lib/kubelet/pki/kubelet-client-current.pem"
	files := map[string]string{
		"/etc/machine-id":                              machineID + "\n",
		"/etc/containerd/config.toml":                  containerdConfig,
		"/etc/cni/net.d/10-kuberoot.conflist":          fmt.Sprintf(cniConfig, workerPodCIDR(node)),
		filepath.Join(kubeDir, "kubelet.yaml"):         workerKubeletConfig(filepath.Join(clusterDir, "ca.crt")),
		filepath.Join(kubeDir, "bootstrap.kubeconfig"): fmt.Sprintf(tokenKubeconfig, m.Server, ca, m.BootstrapToken),
		// kube-proxy acts with the node's own identity, which the cluster binds to the proxier role.
		filepath.Join(kubeDir, "kube-proxy.kubeconfig"): fmt.Sprintf(fileKubeconfig, m.Server, ca, kubeletPKI, kubeletPKI),
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
	for _, dir := range []string{"/var/lib/kubelet", "/var/lib/containerd", "/var/log/kuberoot"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// workerPodCIDR gives each node its own pod range, picked from the node address
// so it is stable without coordination: 10.244.<last octet>.0/24.
func workerPodCIDR(node nodeInfo) string {
	ip := node.ip.To4()
	return fmt.Sprintf("10.244.%d.0/24", ip[3])
}

// podSubnetLabel publishes the node's pod subnet for the other nodes' routes.
func podSubnetLabel(cidr string) string {
	return "kuberoot.dev/pod-subnet=" + strings.Replace(cidr, "/", "-", 1)
}

func workerServices(node nodeInfo) []service {
	ip := node.ip.String()
	return []service{
		{name: "kuberoot-node", args: []string{"/usr/bin/kuberoot-node",
			"--node-name=" + node.name,
			"--advertise-address=" + ip,
			"--tls-cert-file=" + pkiPath("node-api.crt"),
			"--tls-private-key-file=" + pkiPath("node-api.key"),
			"--client-ca-file=" + pkiPath("node-ca.crt"),
			"--cluster-tls-cert-file=" + filepath.Join(clusterDir, "node-api.crt"),
			"--cluster-tls-private-key-file=" + filepath.Join(clusterDir, "node-api.key"),
			"--proxy-trust-ca-file=" + filepath.Join(clusterDir, "ca.crt"),
			"--routes-kubeconfig=/var/lib/kubelet/kubeconfig",
		}},
		{name: "containerd", args: []string{"/usr/bin/containerd", "--config", "/etc/containerd/config.toml"}},
		{name: "kubelet", args: []string{"/usr/bin/kubelet",
			"--config=" + kubeDir + "/kubelet.yaml",
			"--bootstrap-kubeconfig=" + kubeDir + "/bootstrap.kubeconfig",
			"--kubeconfig=/var/lib/kubelet/kubeconfig",
			"--cert-dir=/var/lib/kubelet/pki",
			"--hostname-override=" + node.name,
			"--node-ip=" + ip,
			"--node-labels=" + podSubnetLabel(workerPodCIDR(node)),
		}},
		{name: "kube-proxy", args: []string{"/usr/bin/kube-proxy",
			"--kubeconfig=" + kubeDir + "/kube-proxy.kubeconfig",
			"--proxy-mode=nftables",
			"--cluster-cidr=" + clusterCIDR,
			"--hostname-override=" + node.name,
		}},
	}
}

func workerKubeletConfig(clientCA string) string {
	cfg := fmt.Sprintf(kubeletConfig, clientCA, clusterDNS, "", "")
	var kept []string
	for _, line := range strings.Split(cfg, "\n") {
		// No serving certificate files: the kubelet makes its own.
		if strings.HasPrefix(line, "tlsCertFile:") || strings.HasPrefix(line, "tlsPrivateKeyFile:") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n") + "rotateCertificates: true\n"
}

const tokenKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: cluster
  cluster:
    server: %s
    certificate-authority-data: %s
users:
- name: bootstrap
  user:
    token: %s
contexts:
- name: bootstrap
  context:
    cluster: cluster
    user: bootstrap
current-context: bootstrap
`

const fileKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: cluster
  cluster:
    server: %s
    certificate-authority-data: %s
users:
- name: node
  user:
    client-certificate: %s
    client-key: %s
contexts:
- name: node
  context:
    cluster: cluster
    user: node
current-context: node
`
