package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
)

// Defaults stay clear of the ranges kubeadm, Talos and Cozystack use, since
// kuberoot nodes often run as VMs inside another cluster.
const (
	defaultPodCIDR     = "10.200.0.0/16"
	defaultServiceCIDR = "10.201.0.0/16"
)

// activeNet is the address plan of the cluster this node runs, for the add-ons.
var activeNet clusterNet

// clusterNet holds the cluster's address ranges and what derives from them.
type clusterNet struct {
	pod     *net.IPNet // all pods of the cluster
	service *net.IPNet
}

func parseClusterNet(podCIDR, serviceCIDR string) (clusterNet, error) {
	if podCIDR == "" {
		podCIDR = defaultPodCIDR
	}
	if serviceCIDR == "" {
		serviceCIDR = defaultServiceCIDR
	}
	_, pod, err := net.ParseCIDR(podCIDR)
	if err != nil {
		return clusterNet{}, fmt.Errorf("pod CIDR %q: %w", podCIDR, err)
	}
	_, svc, err := net.ParseCIDR(serviceCIDR)
	if err != nil {
		return clusterNet{}, fmt.Errorf("service CIDR %q: %w", serviceCIDR, err)
	}
	if ones, _ := pod.Mask.Size(); ones > 22 {
		return clusterNet{}, fmt.Errorf("pod CIDR %s is too small: each node takes a /24", podCIDR)
	}
	return clusterNet{pod: pod, service: svc}, nil
}

// serviceIP is the n-th address of the service range: 1 is the kubernetes
// Service, 10 is cluster DNS.
func (c clusterNet) serviceIP(n uint32) net.IP {
	base := binary.BigEndian.Uint32(c.service.IP.To4())
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, base+n)
	return ip
}

func (c clusterNet) apiServiceIP() net.IP { return c.serviceIP(1) }
func (c clusterNet) dnsIP() net.IP        { return c.serviceIP(10) }

// nodeSubnet is a node's /24 of the pod range. The control plane takes the
// first one; others are picked by the last octet of the node address, which
// is stable without coordination as long as nodes share one /24 network.
func (c clusterNet) nodeSubnet(node nodeInfo, controlPlane bool) string {
	ones, _ := c.pod.Mask.Size()
	slots := uint32(1) << (24 - ones)
	index := uint32(0)
	if !controlPlane {
		index = uint32(node.ip.To4()[3]) % slots
		if index == 0 {
			index = 1 % slots
		}
	}
	base := binary.BigEndian.Uint32(c.pod.IP.To4())
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, base+index<<8)
	return ip.String() + "/24"
}

func (c clusterNet) String() string {
	return fmt.Sprintf("pods %s, services %s", c.pod, c.service)
}

// podSubnetLabel publishes the node's pod subnet for the other nodes' routes.
func podSubnetLabel(cidr string) string {
	return "kuberoot.dev/pod-subnet=" + strings.Replace(cidr, "/", "-", 1)
}
