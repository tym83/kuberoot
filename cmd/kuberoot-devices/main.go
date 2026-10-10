// Command kuberoot-devices makes a gateway node run its devices.kuberoot.dev
// resources: it reads the devices, evaluates the routes and publishes what
// they send, with changes on trial under a Safeguard.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/tym83/kuberoot/pkg/devices"
)

func main() {
	kubeconfig := flag.String("kubeconfig", "", "credentials for the node's API server")
	stateDir := flag.String("state-dir", "/var/lib/kuberoot/devices", "what survives a reboot: the confirmed configuration")
	klog.InitFlags(nil)
	flag.Parse()

	cfg, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		klog.Fatalf("kubeconfig: %v", err)
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("client: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := &devices.Controller{Client: client, StateDir: *stateDir}
	if err := c.Run(ctx); err != nil {
		klog.Fatal(err)
	}
}
