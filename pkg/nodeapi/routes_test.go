package nodeapi

import (
	"net"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testNode(name, podCIDR, ip string) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{PodCIDR: podCIDR},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: ip}}},
	}
}

func TestDesiredRoutesTrustOnlyAssignedSubnetsInsideThePodRange(t *testing.T) {
	_, podRange, _ := net.ParseCIDR("10.200.0.0/16")
	nodes := []corev1.Node{
		testNode("self", "10.200.0.0/24", "192.168.100.11"),
		testNode("worker", "10.200.1.0/24", "192.168.100.12"),
		testNode("outside", "0.0.0.0/1", "192.168.100.13"),      // not in the pod range
		testNode("services", "10.201.0.0/16", "192.168.100.14"), // not in the pod range
		testNode("dup-a", "10.200.5.0/24", "192.168.100.15"),
		testNode("dup-b", "10.200.5.0/24", "192.168.100.16"), // the same subnet twice
		testNode("unassigned", "", "192.168.100.17"),
	}
	got := desiredRoutes(nodes, "self", podRange)
	if len(got) != 1 || !got["10.200.1.0/24"].Equal(net.ParseIP("192.168.100.12")) {
		t.Errorf("routes = %v; want only 10.200.1.0/24 via 192.168.100.12", got)
	}
}
