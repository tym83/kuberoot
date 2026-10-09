package nodeapi

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// approveKubeletServing approves kubelet serving certificate requests that
// match the node they come from, so the API server can verify kubelets.
// Kubernetes leaves this approval to the cluster operator; kuberoot does it here.
func approveKubeletServing(ctx context.Context, client kubernetes.Interface, reserved []*net.IPNet) {
	for ctx.Err() == nil {
		csrs, err := client.CertificatesV1().CertificateSigningRequests().List(ctx, metav1.ListOptions{})
		if err == nil {
			for i := range csrs.Items {
				csr := &csrs.Items[i]
				if csr.Spec.SignerName != certificatesv1.KubeletServingSignerName || decided(csr) {
					continue
				}
				if err := checkServingRequest(ctx, client, csr, reserved); err != nil {
					klog.Infof("kubelet serving CSR %s not approved: %v", csr.Name, err)
					continue
				}
				csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
					Type: certificatesv1.CertificateApproved, Status: corev1.ConditionTrue,
					Reason: "KuberootNodeMatch", Message: "requested by the node it names, for its own addresses",
				})
				if _, err := client.CertificatesV1().CertificateSigningRequests().UpdateApproval(ctx, csr.Name, csr, metav1.UpdateOptions{}); err != nil {
					klog.Errorf("approve %s: %v", csr.Name, err)
				}
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

func decided(csr *certificatesv1.CertificateSigningRequest) bool {
	for _, c := range csr.Status.Conditions {
		if c.Type == certificatesv1.CertificateApproved || c.Type == certificatesv1.CertificateDenied {
			return true
		}
	}
	return false
}

// checkServingRequest accepts a request only from system:node:<name> for a
// certificate naming that node and nothing else: its plain name, and the
// internal addresses it reports outside the cluster's own ranges. The cluster
// CA signs it, so a certificate for a service name or a service address would
// let a node pose as that service.
func checkServingRequest(ctx context.Context, client kubernetes.Interface, csr *certificatesv1.CertificateSigningRequest, reserved []*net.IPNet) error {
	nodeName, ok := strings.CutPrefix(csr.Spec.Username, "system:node:")
	if !ok || !slices.Contains(csr.Spec.Groups, "system:nodes") {
		return fmt.Errorf("requester %s is not a node", csr.Spec.Username)
	}
	block, _ := pem.Decode(csr.Spec.Request)
	if block == nil {
		return fmt.Errorf("request is not PEM")
	}
	req, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return err
	}
	if req.Subject.CommonName != csr.Spec.Username {
		return fmt.Errorf("common name %q does not match the requester", req.Subject.CommonName)
	}
	node, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	internal := map[string]bool{}
	for _, a := range node.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			internal[a.Address] = true
		}
	}
	for _, ip := range req.IPAddresses {
		if !internal[ip.String()] {
			return fmt.Errorf("address %s is not an internal address of the node", ip)
		}
		for _, r := range reserved {
			if r.Contains(ip) {
				return fmt.Errorf("address %s is in the cluster range %s", ip, r)
			}
		}
	}
	for _, dns := range req.DNSNames {
		if dns != nodeName || strings.Contains(dns, ".") {
			return fmt.Errorf("name %s is not the node's plain name", dns)
		}
	}
	if len(req.EmailAddresses) > 0 || len(req.URIs) > 0 {
		return fmt.Errorf("only names and addresses may be requested")
	}
	for _, u := range csr.Spec.Usages {
		switch u {
		case certificatesv1.UsageServerAuth, certificatesv1.UsageDigitalSignature, certificatesv1.UsageKeyEncipherment:
		default:
			return fmt.Errorf("usage %s is not for serving", u)
		}
	}
	return nil
}
