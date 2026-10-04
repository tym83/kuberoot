package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
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

// createPKI issues the cluster CA and every certificate the node services use.
func createPKI(node nodeInfo) error {
	if err := os.MkdirAll(pkiDir, 0o700); err != nil {
		return err
	}
	ca, err := newAuthority("kuberoot-ca")
	if err != nil {
		return err
	}
	if err := writePEM("ca.crt", "CERTIFICATE", ca.cert.Raw); err != nil {
		return err
	}
	if err := writeKey("ca.key", ca.key); err != nil {
		return err
	}
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

	server := []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	client := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	specs := []certSpec{
		{file: "apiserver", commonName: "kube-apiserver", usages: server,
			dnsNames: []string{"kubernetes", "kubernetes.default", "kubernetes.default.svc",
				"kubernetes.default.svc.cluster.local", "localhost", node.name},
			ips: []net.IP{net.ParseIP("10.96.0.1"), net.ParseIP("127.0.0.1"), node.ip}},
		{file: "apiserver-kubelet-client", commonName: "kube-apiserver-kubelet-client", orgs: []string{"system:masters"}, usages: client},
		{file: "admin", commonName: "kuberoot-admin", orgs: []string{"system:masters"}, usages: client},
		{file: "controller-manager", commonName: "system:kube-controller-manager", usages: client},
		{file: "scheduler", commonName: "system:kube-scheduler", usages: client},
		{file: "kube-proxy", commonName: "system:kube-proxy", usages: client},
		{file: "kubelet-client", commonName: "system:node:" + node.name, orgs: []string{"system:nodes"}, usages: client},
		{file: "kubelet-server", commonName: node.name, usages: server,
			dnsNames: []string{node.name, "localhost"}, ips: []net.IP{node.ip, net.ParseIP("127.0.0.1")}},
	}
	for _, s := range specs {
		if err := ca.issue(s); err != nil {
			return fmt.Errorf("issue %s: %w", s.file, err)
		}
	}
	return createNodePKI(node)
}

// createNodePKI issues the node's own trust root, separate from the cluster's:
// the node API exists before a cluster does and outlives it.
func createNodePKI(node nodeInfo) error {
	ca, err := newAuthority("kuberoot-node-ca " + node.name)
	if err != nil {
		return err
	}
	if err := writePEM("node-ca.crt", "CERTIFICATE", ca.cert.Raw); err != nil {
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
	return os.WriteFile(filepath.Join(pkiDir, name), pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600)
}

func pkiPath(name string) string { return filepath.Join(pkiDir, name) }
