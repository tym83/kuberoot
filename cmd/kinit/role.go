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
func configure(node nodeInfo, cfg bootConfig) ([]service, bool, error) {
	m, err := membership.Load()
	if err != nil {
		return nil, false, fmt.Errorf("membership: %w", err)
	}
	if m == nil {
		cn, err := parseClusterNet(cfg.podCIDR, cfg.serviceCIDR)
		if err != nil {
			return nil, false, err
		}
		if err := writeNodeConfig(node, cn); err != nil {
			return nil, false, err
		}
		activeNet = cn
		return nodeServices(node, cn, cfg), true, nil
	}
	log.Printf("joining %s as %s", m.Server, strings.ToLower(m.Role))
	// A member takes the cluster's address ranges, whatever its own boot arguments say.
	cn, err := parseClusterNet(m.PodCIDR, m.ServiceCIDR)
	if err != nil {
		return nil, false, err
	}
	if err := writeWorkerConfig(node, *m, cn); err != nil {
		return nil, false, err
	}
	return workerServices(node, cn), false, nil
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
	services, controlPlane, err := configure(node, cfg)
	if err != nil {
		log.Printf("reconfigure: %v", err)
		return
	}
	startServices(services, cfg)
	if controlPlane {
		go applyAddons(cfg, node)
	}
}

func writeWorkerConfig(node nodeInfo, m nodev1.MembershipSpec, cn clusterNet) error {
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
		"/etc/cni/net.d/10-kuberoot.conflist":          fmt.Sprintf(cniConfig, cn.nodeSubnet(node, false)),
		filepath.Join(kubeDir, "kubelet.yaml"):         workerKubeletConfig(filepath.Join(clusterDir, "ca.crt"), cn),
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

func workerServices(node nodeInfo, cn clusterNet) []service {
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
			"--node-labels=" + podSubnetLabel(cn.nodeSubnet(node, false)),
		}},
		{name: "kube-proxy", after: kubeletCertIssued, args: []string{"/usr/bin/kube-proxy",
			"--kubeconfig=" + kubeDir + "/kube-proxy.kubeconfig",
			"--proxy-mode=nftables",
			"--cluster-cidr=" + cn.pod.String(),
			"--hostname-override=" + node.name,
		}},
	}
}

// kubeletCertIssued: kube-proxy on a worker uses the kubelet's client
// certificate, which exists only once the kubelet has bootstrapped.
func kubeletCertIssued() bool {
	_, err := os.Stat("/var/lib/kubelet/pki/kubelet-client-current.pem")
	return err == nil
}

func workerKubeletConfig(clientCA string, cn clusterNet) string {
	cfg := fmt.Sprintf(kubeletConfig, clientCA, cn.dnsIP(), "", "")
	var kept []string
	for _, line := range strings.Split(cfg, "\n") {
		// No serving certificate files: the kubelet requests one from the cluster.
		if strings.HasPrefix(line, "tlsCertFile:") || strings.HasPrefix(line, "tlsPrivateKeyFile:") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n") + "rotateCertificates: true\nserverTLSBootstrap: true\n"
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
