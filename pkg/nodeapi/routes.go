package nodeapi

import (
	"context"
	"net"
	"os"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

// PodSubnetLabel publishes a node's pod subnet, "10.244.5.0-24", so the other
// nodes can route to it. Label values cannot hold a slash.
const PodSubnetLabel = "kuberoot.dev/pod-subnet"

// syncRoutes keeps a route to every other node's pod subnet through that node:
// the nodes share a network, so pods reach each other without an overlay.
func syncRoutes(ctx context.Context, kubeconfig, self string) {
	var client kubernetes.Interface
	for ctx.Err() == nil {
		if client == nil {
			client = clientFrom(kubeconfig)
		}
		if client != nil {
			if nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
				for _, n := range nodes.Items {
					if n.Name != self {
						routeTo(n)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
	}
}

func clientFrom(kubeconfig string) kubernetes.Interface {
	if _, err := os.Stat(kubeconfig); err != nil {
		return nil // a joining worker gets its kubeconfig once the kubelet bootstraps
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil
	}
	return client
}

func routeTo(n corev1.Node) {
	subnet := strings.Replace(n.Labels[PodSubnetLabel], "-", "/", 1)
	_, dst, err := net.ParseCIDR(subnet)
	if err != nil {
		return
	}
	var gw net.IP
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			gw = net.ParseIP(a.Address)
		}
	}
	if gw == nil {
		return
	}
	if err := netlink.RouteReplace(&netlink.Route{Dst: dst, Gw: gw}); err != nil {
		klog.V(2).Infof("route %s via %s: %v", dst, gw, err)
	}
}
