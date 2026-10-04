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
	klog.InitFlags(nil)
	flag.Parse()

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
