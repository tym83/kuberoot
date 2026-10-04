package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"
)

const addonsDir = "/usr/share/kuberoot/addons"

// applyAddons waits for the API server and applies the bundled cluster add-ons.
func applyAddons(cfg bootConfig) {
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
	for {
		cmd := exec.Command("/usr/bin/kubectl", "--kubeconfig", kubeDir+"/admin.kubeconfig", "apply", "--server-side", "-f", addonsDir)
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
