package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tym83/kuberoot/pkg/atomicfile"
	"github.com/vishvananda/netlink"

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
		key, err := encryptionKey()
		if err != nil {
			return err
		}
		files[filepath.Join(kubeDir, "admission.yaml")] = admissionConfig
		files[filepath.Join(kubeDir, "audit-policy.yaml")] = auditPolicy
		files[pkiPath("encryption.yaml")] = fmt.Sprintf(encryptionConfig, key)
		activeNet = r.net
		if err := writeFiles(files, "/var/lib/kine"); err != nil {
			return err
		}
		// kine listens on a socket only root reaches, not on a TCP port any
		// host-network pod could talk to.
		return os.MkdirAll("/run/kine", 0o700)
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
	// subnet the control plane assigns to this node. With pod-network=none a
	// CNI package (Cilium) writes its own configuration instead.
	"containers": func(r roleContext) error {
		// A configuration left from a previous role or cluster has the wrong subnet.
		_ = os.RemoveAll("/etc/cni/net.d")
		files := map[string]string{"/etc/containerd/config.toml": containerdConfig}
		if podNetworkMode == "none" {
			files["/etc/containerd/config.toml"] = strings.Replace(containerdConfig, cniTemplateLine, "", 1)
		} else {
			files["/etc/cni/kuberoot.conflist.tmpl"] = strings.Replace(cniConfig, "__MTU__", strconv.Itoa(podMTU()), 1)
		}
		return writeFiles(files, "/var/lib/containerd", "/etc/cni/net.d")
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

// podMTU leaves room in the node's MTU for the VXLAN header pod traffic
// between nodes is carried in.
func podMTU() int {
	mtu := 1500
	if routes, err := netlink.RouteList(nil, netlink.FAMILY_V4); err == nil {
		for _, r := range routes {
			if r.Dst == nil || r.Dst.IP.IsUnspecified() {
				if l, err := netlink.LinkByIndex(r.LinkIndex); err == nil && l.Attrs().MTU > 0 {
					mtu = l.Attrs().MTU
					break
				}
			}
		}
	}
	return mtu - vxlanOverhead
}

// vxlanOverhead matches nodeapi.VXLANOverhead; kinit does not import the
// node API server for one number.
const vxlanOverhead = 50

// encryptionKey is the key Secrets are encrypted with in the store, made once
// and kept with the cluster's PKI.
func encryptionKey() (string, error) {
	path := pkiPath("encryption.key")
	if raw, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(raw)), nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	return encoded, atomicfile.WriteFile(path, []byte(encoded+"\n"), 0o600)
}

// admissionConfig makes the baseline Pod Security Standard the default
// everywhere but kube-system; a namespace that needs more asks for it with
// the usual pod-security.kubernetes.io labels.
const admissionConfig = `apiVersion: apiserver.config.k8s.io/v1
kind: AdmissionConfiguration
plugins:
- name: PodSecurity
  configuration:
    apiVersion: pod-security.admission.config.k8s.io/v1
    kind: PodSecurityConfiguration
    defaults:
      enforce: baseline
      enforce-version: latest
      warn: restricted
      warn-version: latest
    exemptions:
      namespaces: [kube-system]
`

// auditPolicy records who changed what; reads stay out of the log.
const auditPolicy = `apiVersion: audit.k8s.io/v1
kind: Policy
omitStages: [RequestReceived]
rules:
- level: None
  verbs: [get, list, watch]
- level: None
  resources:
  - group: coordination.k8s.io
    resources: [leases]
  - group: ""
    resources: [events]
- level: Metadata
`

const encryptionConfig = `apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
- resources: [secrets]
  providers:
  - secretbox:
      keys:
      - name: key1
        secret: %s
  - identity: {}
`

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
