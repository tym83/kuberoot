package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	nodev1 "github.com/tym83/kuberoot/pkg/apis/node/v1alpha1"
)

// roleContext is what generators and service templates know about the node
// and the role it is configured for.
type roleContext struct {
	node         nodeInfo
	net          clusterNet
	controlPlane bool
	member       *nodev1.MembershipSpec // set for a member of another node's cluster
}

// generators write what a role needs on disk; a distribution profile picks
// the ones its roles use.
var generators = map[string]func(roleContext) error{
	// node: identity and the node API's own PKI; every role needs it.
	"node": func(r roleContext) error {
		if err := createNodePKI(r.node); err != nil {
			return err
		}
		machineID, err := persistentMachineID()
		if err != nil {
			return err
		}
		nodeAdmin, err := kubeconfig(nodeAPIServer, nodeServingCA, r.node.name, "node-admin")
		if err != nil {
			return err
		}
		return writeFiles(map[string]string{"/etc/machine-id": machineID + "\n", nodeAdminKubeconfig: nodeAdmin},
			"/var/log/kuberoot")
	},
	// cluster: the PKI and component credentials of a cluster this node runs.
	"cluster": func(r roleContext) error {
		if err := createClusterPKI(r.node, r.net); err != nil {
			return err
		}
		files := map[string]string{}
		for _, user := range []string{"admin", "controller-manager", "scheduler", "kube-proxy", "kubelet-client", "node-api-delegation", "intents"} {
			kc, err := kubeconfig(apiServer, "ca.crt", "kuberoot", user)
			if err != nil {
				return err
			}
			files[filepath.Join(kubeDir, user+".kubeconfig")] = kc
		}
		activeNet = r.net
		return writeFiles(files, "/var/lib/kine")
	},
	// member: what a member receives from its cluster, and its bootstrap credentials.
	"member": func(r roleContext) error {
		if r.member == nil {
			return fmt.Errorf("the member generator needs a membership")
		}
		m := r.member
		if err := os.MkdirAll(clusterDir, 0o700); err != nil {
			return err
		}
		for name, content := range map[string]string{
			"ca.crt": m.ClusterCA, "front-proxy-ca.crt": m.FrontProxyCA,
			"node-api.crt": m.NodeAPICert, "node-api.key": m.NodeAPIKey,
		} {
			if err := os.WriteFile(filepath.Join(clusterDir, name), []byte(content), 0o600); err != nil {
				return err
			}
		}
		ca := base64.StdEncoding.EncodeToString([]byte(m.ClusterCA))
		kubeletPKI := "/var/lib/kubelet/pki/kubelet-client-current.pem"
		return writeFiles(map[string]string{
			filepath.Join(kubeDir, "bootstrap.kubeconfig"): fmt.Sprintf(tokenKubeconfig, m.Server, ca, m.BootstrapToken),
			// kube-proxy acts with the node's own identity, which the cluster binds to the proxier role.
			filepath.Join(kubeDir, "kube-proxy.kubeconfig"): fmt.Sprintf(fileKubeconfig, m.Server, ca, kubeletPKI, kubeletPKI),
		})
	},
	// containers: containerd and the bridge CNI template, filled with the pod
	// subnet the control plane assigns to this node.
	"containers": func(r roleContext) error {
		// A configuration left from a previous role or cluster has the wrong subnet.
		_ = os.RemoveAll("/etc/cni/net.d")
		return writeFiles(map[string]string{
			"/etc/containerd/config.toml":     containerdConfig,
			"/etc/cni/kuberoot.conflist.tmpl": cniConfig,
		}, "/var/lib/containerd", "/etc/cni/net.d")
	},
	// kubelet: its configuration, with serving certificates from the cluster CA
	// on the control plane and requested through the CSR API on members.
	"kubelet": func(r roleContext) error {
		cfg := fmt.Sprintf(kubeletConfig, pkiPath("ca.crt"), r.net.dnsIP(), pkiPath("kubelet-server.crt"), pkiPath("kubelet-server.key"))
		if r.member != nil {
			cfg = workerKubeletConfig(filepath.Join(clusterDir, "ca.crt"), r.net)
		}
		return writeFiles(map[string]string{filepath.Join(kubeDir, "kubelet.yaml"): cfg}, "/var/lib/kubelet")
	},
}

func writeFiles(files map[string]string, dirs ...string) error {
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}
