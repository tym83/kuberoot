package nodeapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"testing"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func servingCSR(t *testing.T, node string, dns []string, ips ...string) *certificatesv1.CertificateSigningRequest {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.CertificateRequest{Subject: pkix.Name{CommonName: "system:node:" + node, Organization: []string{"system:nodes"}}, DNSNames: dns}
	for _, ip := range ips {
		tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP(ip))
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		t.Fatal(err)
	}
	return &certificatesv1.CertificateSigningRequest{Spec: certificatesv1.CertificateSigningRequestSpec{
		SignerName: certificatesv1.KubeletServingSignerName,
		Username:   "system:node:" + node, Groups: []string{"system:nodes", "system:authenticated"},
		Request: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}),
		Usages:  []certificatesv1.KeyUsage{certificatesv1.UsageDigitalSignature, certificatesv1.UsageServerAuth},
	}}
}

func TestServingRequestChecks(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker"},
		Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{
			{Type: corev1.NodeInternalIP, Address: "192.168.100.12"},
			{Type: corev1.NodeInternalIP, Address: "10.201.0.1"}, // a lie: the API service address
			{Type: corev1.NodeExternalIP, Address: "203.0.113.5"},
			{Type: corev1.NodeHostName, Address: "kubernetes.default.svc"},
		}},
	})
	var reserved []*net.IPNet
	for _, c := range []string{"10.200.0.0/16", "10.201.0.0/16"} {
		_, r, _ := net.ParseCIDR(c)
		reserved = append(reserved, r)
	}
	cases := []struct {
		name string
		csr  *certificatesv1.CertificateSigningRequest
		ok   bool
	}{
		{"own name and internal address", servingCSR(t, "worker", []string{"worker"}, "192.168.100.12"), true},
		{"service name", servingCSR(t, "worker", []string{"kubernetes.default.svc"}, "192.168.100.12"), false},
		{"service address it claims", servingCSR(t, "worker", []string{"worker"}, "10.201.0.1"), false},
		{"external address", servingCSR(t, "worker", []string{"worker"}, "203.0.113.5"), false},
		{"another node's name", servingCSR(t, "worker", []string{"control-plane"}), false},
	}
	for _, c := range cases {
		err := checkServingRequest(context.Background(), client, c.csr, reserved)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}
