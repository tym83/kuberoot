package main

import (
	"encoding/binary"
	"fmt"
	"net"
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
		return clusterNet{}, fmt.Errorf("pod CIDR %s is too small: the control plane hands each node a /24", podCIDR)
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

func (c clusterNet) String() string {
	return fmt.Sprintf("pods %s, services %s", c.pod, c.service)
}
