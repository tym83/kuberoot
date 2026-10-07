// Command kuberoot-node serves the node API: the node's operating system as
// Kubernetes resources, available before any cluster exists.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"strings"

	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"

	"github.com/tym83/kuberoot/pkg/distro"
	"github.com/tym83/kuberoot/pkg/nodeapi"
)

func main() {
	var o nodeapi.Options
	var kubeconfigs string
	hostname, _ := os.Hostname()
	flag.StringVar(&o.NodeName, "node-name", hostname, "name of this node")
	flag.IntVar(&o.BindPort, "port", 50000, "port to serve on")
	flag.StringVar(&o.CertFile, "tls-cert-file", "", "serving certificate")
	flag.StringVar(&o.KeyFile, "tls-private-key-file", "", "serving key")
	flag.StringVar(&o.ClientCA, "client-ca-file", "", "CA that signs node admin certificates")
	flag.StringVar(&kubeconfigs, "kubeconfigs", "", "name=path pairs of cluster credentials to serve, comma separated")
	flag.StringVar(&o.RequestHeaderCA, "requestheader-client-ca-file", "", "front proxy CA of the cluster API server")
	flag.StringVar(&o.ClusterCertFile, "cluster-tls-cert-file", "", "serving certificate signed by the cluster CA")
	flag.StringVar(&o.ClusterKeyFile, "cluster-tls-private-key-file", "", "key of the cluster serving certificate")
	flag.StringVar(&o.ClusterKubeconfig, "cluster-kubeconfig", "", "credentials for delegated authorization checks")
	flag.StringVar(&o.Advertise, "advertise-address", "", "address of this node in the cluster")
	flag.StringVar(&o.ClusterCAFile, "cluster-ca-file", "", "cluster CA certificate (control plane node)")
	flag.StringVar(&o.ClusterCAKeyFile, "cluster-ca-key-file", "", "cluster CA key, to issue join tickets (control plane node)")
	flag.StringVar(&o.AdminKubeconfig, "admin-kubeconfig", "", "cluster admin credentials (control plane node)")
	flag.StringVar(&o.ProxyClientCert, "proxy-client-cert-file", "", "identity for reaching member node APIs")
	flag.StringVar(&o.ProxyClientKey, "proxy-client-key-file", "", "key of the proxy identity")
	flag.StringVar(&o.ProxyTrustCA, "proxy-trust-ca-file", "", "CA of the control plane proxy identity (member node)")
	flag.StringVar(&o.RoutesKubeconfig, "routes-kubeconfig", "", "credentials to read nodes and route pod subnets between them")
	flag.StringVar(&o.PodCIDR, "pod-cidr", "", "pod address range of the cluster (control plane node)")
	flag.StringVar(&o.PodNetwork, "pod-network", nodeapi.PodNetworkVXLAN, "how pod traffic crosses between nodes: vxlan, or host-gw on a plain layer-2 network")
	flag.StringVar(&o.ServiceCIDR, "service-cidr", "", "service address range of the cluster (control plane node)")
	profilePath := flag.String("profile", distro.Path, "distribution profile: which node resources to serve")
	klog.InitFlags(nil)
	flag.Parse()
	if p, err := distro.Load(*profilePath); err == nil {
		o.Resources = p.Spec.NodeAPI.Resources
	} else {
		klog.Infof("no distribution profile (%v): serving every node resource", err)
	}

	o.Kubeconfig = map[string]string{}
	for _, pair := range strings.Split(kubeconfigs, ",") {
		if name, path, ok := strings.Cut(pair, "="); ok {
			o.Kubeconfig[name] = path
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), unix.SIGTERM, unix.SIGINT)
	defer cancel()
	if err := nodeapi.Run(ctx, o); err != nil {
		klog.Fatal(err)
	}
}
