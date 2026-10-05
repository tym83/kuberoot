package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	addonsDir          = "/usr/share/kuberoot/addons"
	generatedAddonsDir = "/run/kuberoot/addons"
)

// renderAddons copies the bundled add-ons next to the generated ones, filling in
// the addresses that depend on the cluster's ranges.
func renderAddons() error {
	entries, err := os.ReadDir(addonsDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(addonsDir, e.Name()))
		if err != nil {
			return err
		}
		out := strings.ReplaceAll(string(raw), "__CLUSTER_DNS__", activeNet.dnsIP().String())
		if err := os.WriteFile(filepath.Join(generatedAddonsDir, e.Name()), []byte(out), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// writeAggregation registers the node API with the cluster API server. The
// Service has no selector: its endpoint is the node itself, not a pod.
func writeAggregation(node nodeInfo) error {
	ca, err := os.ReadFile(pkiPath("ca.crt"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(generatedAddonsDir, 0o755); err != nil {
		return err
	}
	manifest := fmt.Sprintf(aggregationTemplate, node.name, node.ip, node.name, base64.StdEncoding.EncodeToString(ca))
	return os.WriteFile(generatedAddonsDir+"/node-api.yaml", []byte(manifest), 0o644)
}

const aggregationTemplate = `apiVersion: v1
kind: Service
metadata:
  name: kuberoot-node
  namespace: kube-system
spec:
  ports:
  - name: https
    port: 50000
    targetPort: 50000
---
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata:
  name: kuberoot-node-%s
  namespace: kube-system
  labels:
    kubernetes.io/service-name: kuberoot-node
addressType: IPv4
ports:
- name: https
  port: 50000
  protocol: TCP
endpoints:
- addresses: ["%s"]
  nodeName: %s
  conditions:
    ready: true
---
apiVersion: apiregistration.k8s.io/v1
kind: APIService
metadata:
  name: v1alpha1.node.kuberoot.dev
spec:
  group: node.kuberoot.dev
  version: v1alpha1
  groupPriorityMinimum: 1000
  versionPriority: 15
  caBundle: %s
  service:
    namespace: kube-system
    name: kuberoot-node
    port: 50000
`

// applyAddons waits for the API server and applies the bundled cluster add-ons.
func applyAddons(cfg bootConfig, node nodeInfo) {
	client, err := adminClient()
	if err != nil {
		log.Printf("addons: %v", err)
		return
	}
	for {
		resp, err := client.Get(apiServer + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(time.Second)
	}
	log.Printf("kube-apiserver ready, uptime %s", uptime())
	if err := writeAggregation(node); err != nil {
		log.Printf("aggregation manifest: %v", err)
	}
	if err := renderAddons(); err != nil {
		log.Printf("add-ons: %v", err)
	}
	for {
		cmd := exec.Command("/usr/bin/kubectl", "--kubeconfig", kubeDir+"/admin.kubeconfig", "apply", "--server-side",
			"-f", generatedAddonsDir)
		cmd.Env = servicePath
		out, err := os.OpenFile("/var/log/kuberoot/addons.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			cmd.Stdout, cmd.Stderr = out, out
		}
		done, err := spawn(cmd)
		if err == nil && (<-done).ExitStatus() == 0 {
			out.Close()
			log.Printf("add-ons applied")
			return
		}
		out.Close()
		time.Sleep(5 * time.Second)
	}
}

// apiServerReady reports whether the local API server answers /readyz.
func apiServerReady() bool {
	client, err := adminClient()
	if err != nil {
		return false
	}
	resp, err := client.Get(apiServer + "/readyz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func adminClient() (*http.Client, error) {
	cert, err := tls.LoadX509KeyPair(pkiPath("admin.crt"), pkiPath("admin.key"))
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(pkiPath("ca.crt"))
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: pool}},
	}, nil
}

// printNodeKubeconfig hands the node admin credentials to a development host over
// the serial console. The installer will take over this job.
func printNodeKubeconfig() {
	raw, err := os.ReadFile(nodeAdminKubeconfig)
	if err != nil {
		return
	}
	os.Stdout.WriteString("-----BEGIN KUBEROOT KUBECONFIG-----\n" + base64.StdEncoding.EncodeToString(raw) + "\n-----END KUBEROOT KUBECONFIG-----\n")
}
