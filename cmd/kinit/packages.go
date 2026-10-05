package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	kubepkgCRDs     = "/usr/share/kuberoot/kubepkg/crds"
	kubepkgRepoKey  = "/usr/share/kuberoot/kubepkg-recipes.pub"
	kubepkgCacheDir = "/var/lib/kubepkg"
)

func kubepkgOperatorArgs(cfg bootConfig) []string {
	args := []string{"/usr/bin/kubepkg-operator",
		"--backend=helm",
		"--cache-dir=" + kubepkgCacheDir + "/cache",
		"--metrics-bind-address=0",
		"--health-probe-bind-address=0",
	}
	if cfg.repoPlainHTTP {
		args = append(args, "--plain-http")
	}
	return args
}

// renderPackages writes the kubepkg CRDs and the cluster's subscription: the
// repository and the CoreDNS package with this cluster's DNS address.
func renderPackages(cfg bootConfig) error {
	entries, err := os.ReadDir(kubepkgCRDs)
	if err != nil {
		return err
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(kubepkgCRDs, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(generatedAddonsDir, "00-"+e.Name()), raw, 0o644); err != nil {
			return err
		}
	}
	key, err := repoKey(cfg)
	if err != nil {
		return err
	}
	var indented strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(key), "\n") {
		indented.WriteString("      " + line + "\n")
	}
	manifest := fmt.Sprintf(packagesTemplate, cfg.repo, indented.String(), activeNet.dnsIP())
	return os.WriteFile(filepath.Join(generatedAddonsDir, "50-packages.yaml"), []byte(manifest), 0o644)
}

func repoKey(cfg bootConfig) (string, error) {
	if cfg.repoKey == "" {
		raw, err := os.ReadFile(kubepkgRepoKey)
		return string(raw), err
	}
	raw, err := base64.StdEncoding.DecodeString(cfg.repoKey)
	if err != nil {
		return "", fmt.Errorf("kuberoot.repo-key: %w", err)
	}
	return string(raw), nil
}

// installDistro installs the distribution's meta package; the kubepkg CLI
// resolves what it requires and the operator installs it. Packages already
// present, like CoreDNS with its address, keep their settings.
func installDistro(cfg bootConfig) {
	pkg := "kuberoot-" + cfg.distro
	for {
		cmd := exec.Command("/usr/bin/kubepkg", "install", pkg, "--yes")
		cmd.Env = append(append([]string{}, servicePath...), "KUBECONFIG="+kubeDir+"/admin.kubeconfig", "HOME="+kubepkgCacheDir)
		out, err := os.OpenFile("/var/log/kuberoot/addons.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			cmd.Stdout, cmd.Stderr = out, out
		}
		done, err := spawn(cmd)
		if err == nil && (<-done).ExitStatus() == 0 {
			out.Close()
			log.Printf("distribution %s requested from %s", pkg, cfg.repo)
			return
		}
		out.Close()
		time.Sleep(10 * time.Second)
	}
}

const packagesTemplate = `apiVersion: kubepkg.dev/v1alpha1
kind: Repository
metadata:
  name: main
spec:
  url: %s
  publicKeys:
    - |
%s---
apiVersion: kubepkg.dev/v1alpha1
kind: Package
metadata:
  name: coredns
spec:
  version: "~1.14"
  components:
    coredns:
      values:
        service:
          clusterIP: "%s"
`
