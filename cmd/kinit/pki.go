package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"github.com/tym83/kuberoot/pkg/atomicfile"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const pkiDir = "/var/lib/kuberoot/pki"

type authority struct {
	cert *x509.Certificate
	key  crypto.Signer
}

type certSpec struct {
	file       string
	commonName string
	orgs       []string
	usages     []x509.ExtKeyUsage
	dnsNames   []string
	ips        []net.IP
}

// createPKI keeps the cluster CA and service account key across reboots and
// reissues every leaf certificate, since the node address may have changed.
func createPKI(node nodeInfo, cn clusterNet) error {
	if err := os.MkdirAll(pkiDir, 0o700); err != nil {
		return err
	}
	ca, err := loadOrCreateAuthority("ca", "kuberoot-ca")
	if err != nil {
		return err
	}
	if _, err := os.Stat(pkiPath("sa.key")); os.IsNotExist(err) {
		saKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		if err := writeKey("sa.key", saKey); err != nil {
			return err
		}
		saPub, err := x509.MarshalPKIXPublicKey(&saKey.PublicKey)
		if err != nil {
			return err
		}
		if err := writePEM("sa.pub", "PUBLIC KEY", saPub); err != nil {
			return err
		}
	}

	server := []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	client := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	specs := []certSpec{
		{file: "apiserver", commonName: "kube-apiserver", usages: server,
			dnsNames: []string{"kubernetes", "kubernetes.default", "kubernetes.default.svc",
				"kubernetes.default.svc.cluster.local", "localhost", node.name},
			ips: []net.IP{cn.apiServiceIP(), net.ParseIP("127.0.0.1"), node.ip}},
		{file: "apiserver-kubelet-client", commonName: "kube-apiserver-kubelet-client", orgs: []string{"system:masters"}, usages: client},
		{file: "admin", commonName: "kuberoot-admin", orgs: []string{"system:masters"}, usages: client},
		{file: "controller-manager", commonName: "system:kube-controller-manager", usages: client},
		{file: "scheduler", commonName: "system:kube-scheduler", usages: client},
		{file: "kube-proxy", commonName: "system:kube-proxy", usages: client},
		// The translator writes the primitives of sealed namespaces; the seal lets
		// this identity through and nobody else, cluster admins included.
		{file: "intents", commonName: "kuberoot:intents", orgs: []string{"system:masters"}, usages: client},
		{file: "kubelet-client", commonName: "system:node:" + node.name, orgs: []string{"system:nodes"}, usages: client},
		{file: "kubelet-server", commonName: node.name, usages: server,
			dnsNames: []string{node.name, "localhost"}, ips: []net.IP{node.ip, net.ParseIP("127.0.0.1")}},
	}
	for _, s := range specs {
		if err := ca.issue(s); err != nil {
			return fmt.Errorf("issue %s: %w", s.file, err)
		}
	}
	if err := createAggregationPKI(ca); err != nil {
		return err
	}
	return createNodePKI(node)
}

// createAggregationPKI lets the cluster API server reach the node API: the
// front proxy identity it forwards requests with, a serving certificate the
// cluster CA vouches for, and the node API's own identity for delegated checks.
func createAggregationPKI(clusterCA *authority) error {
	frontProxy, err := loadOrCreateAuthority("front-proxy-ca", "kuberoot-front-proxy-ca")
	if err != nil {
		return err
	}
	client := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	if err := frontProxy.issue(certSpec{file: "front-proxy-client", commonName: "front-proxy-client", usages: client}); err != nil {
		return err
	}
	for _, s := range []certSpec{
		{file: "node-api-cluster", commonName: nodeAPIServiceDNS, usages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			dnsNames: []string{nodeAPIServiceDNS, "kuberoot-node.kube-system.svc.cluster.local"}},
		{file: "node-api-delegation", commonName: "kuberoot:node-api", usages: client},
		// The control plane node API reaches member node APIs with this identity.
		{file: "node-api-proxy", commonName: "kuberoot:node-api-proxy", usages: client},
	} {
		if err := clusterCA.issue(s); err != nil {
			return fmt.Errorf("issue %s: %w", s.file, err)
		}
	}
	return nil
}

// createNodePKI issues the node's own trust root, separate from the cluster's:
// the node API exists before a cluster does and outlives it.
func createNodePKI(node nodeInfo) error {
	ca, err := loadOrCreateAuthority("node-ca", "kuberoot-node-ca "+node.name)
	if err != nil {
		return err
	}
	for _, s := range []certSpec{
		{file: "node-api", commonName: node.name, usages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			dnsNames: []string{node.name, "localhost"}, ips: []net.IP{node.ip, net.ParseIP("127.0.0.1")}},
		{file: "node-admin", commonName: "kuberoot-node-admin", orgs: []string{"kuberoot:node-admins"},
			usages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
	} {
		if err := ca.issue(s); err != nil {
			return fmt.Errorf("issue %s: %w", s.file, err)
		}
	}
	return nil
}

// loadOrCreateAuthority reuses <file>.crt/.key when present, so credentials
// handed out earlier stay valid after a reboot.
func loadOrCreateAuthority(file, name string) (*authority, error) {
	if certPEM, err := os.ReadFile(pkiPath(file + ".crt")); err == nil {
		keyPEM, err := os.ReadFile(pkiPath(file + ".key"))
		if err != nil {
			return nil, err
		}
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", file, err)
		}
		cert, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return nil, err
		}
		return &authority{cert: cert, key: pair.PrivateKey.(crypto.Signer)}, nil
	}
	ca, err := newAuthority(name)
	if err != nil {
		return nil, err
	}
	// Key first: a power cut between the two leaves no certificate, so the
	// next boot makes a fresh pair instead of finding a certificate without a key.
	if err := writeKey(file+".key", ca.key); err != nil {
		return nil, err
	}
	return ca, writePEM(file+".crt", "CERTIFICATE", ca.cert.Raw)
}

func newAuthority(name string) (*authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &authority{cert: cert, key: key}, nil
}

func (a *authority) issue(s certSpec) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: s.commonName, Organization: s.orgs},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  s.usages,
		DNSNames:     s.dnsNames,
		IPAddresses:  s.ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return err
	}
	if err := writePEM(s.file+".crt", "CERTIFICATE", der); err != nil {
		return err
	}
	return writeKey(s.file+".key", key)
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

func writeKey(name string, key crypto.Signer) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	return writePEM(name, "PRIVATE KEY", der)
}

func writePEM(name, kind string, der []byte) error {
	return atomicfile.WriteFile(filepath.Join(pkiDir, name), pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600)
}

func pkiPath(name string) string { return filepath.Join(pkiDir, name) }
