package nodeapi

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

// syncRoutes keeps a route to every other node's pod subnet through that node,
// so pods reach each other without an overlay. Subnets come from the Node
// spec, which the control plane assigns and a node cannot change; only
// subnets inside the cluster's pod range, claimed by exactly one node, get a
// route. Routes into the pod range that no node claims any more are removed.
func syncRoutes(ctx context.Context, kubeconfig, self string, podRange *net.IPNet) {
	var client kubernetes.Interface
	for ctx.Err() == nil {
		if client == nil {
			client = clientFrom(kubeconfig)
		}
		if client != nil && podRange != nil {
			if nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
				applyRoutes(desiredRoutes(nodes.Items, self, podRange), podRange)
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
	}
}

// desiredRoutes maps each other node's pod subnet to that node's address.
func desiredRoutes(nodes []corev1.Node, self string, podRange *net.IPNet) map[string]net.IP {
	claims := map[string]int{}
	routes := map[string]net.IP{}
	for _, n := range nodes {
		_, subnet, err := net.ParseCIDR(n.Spec.PodCIDR)
		if err != nil || !within(subnet, podRange) {
			continue
		}
		claims[subnet.String()]++
		if n.Name == self {
			continue
		}
		for _, a := range n.Status.Addresses {
			if a.Type == corev1.NodeInternalIP {
				if gw := net.ParseIP(a.Address); gw != nil && !podRange.Contains(gw) {
					routes[subnet.String()] = gw
				}
			}
		}
	}
	for subnet, count := range claims {
		if count > 1 {
			klog.Errorf("pod subnet %s is claimed by %d nodes; not routing it", subnet, count)
			delete(routes, subnet)
		}
	}
	return routes
}

func within(subnet, outer *net.IPNet) bool {
	inner, _ := subnet.Mask.Size()
	outerOnes, _ := outer.Mask.Size()
	return outer.Contains(subnet.IP) && inner >= outerOnes
}

func applyRoutes(desired map[string]net.IP, podRange *net.IPNet) {
	existing, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return
	}
	for _, r := range existing {
		if r.Dst == nil || r.Gw == nil || !within(r.Dst, podRange) {
			continue
		}
		if gw, ok := desired[r.Dst.String()]; !ok || !gw.Equal(r.Gw) {
			if err := netlink.RouteDel(&r); err != nil {
				klog.V(2).Infof("remove route %s: %v", r.Dst, err)
			}
		}
	}
	for subnet, gw := range desired {
		_, dst, _ := net.ParseCIDR(subnet)
		if err := netlink.RouteReplace(&netlink.Route{Dst: dst, Gw: gw}); err != nil {
			klog.V(2).Infof("route %s via %s: %v", dst, gw, err)
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
