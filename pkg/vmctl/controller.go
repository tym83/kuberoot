package vmctl

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	nodev1 "github.com/tym83/kuberoot/pkg/apis/node/v1alpha1"
	routerv1 "github.com/tym83/kuberoot/pkg/apis/router/v1alpha1"
	v1 "github.com/tym83/kuberoot/pkg/apis/vm/v1alpha1"
	"github.com/tym83/kuberoot/pkg/router"
	"github.com/tym83/kuberoot/pkg/vm"
)

const finalizer = "vm.kuberoot.dev/cleanup"

var (
	vmsGVR      = v1.GroupVersion.WithResource("virtualmachines")
	volumesGVR  = schema.GroupVersionResource{Group: "node.kuberoot.dev", Version: "v1alpha1", Resource: "volumes"}
	machinesGVR = schema.GroupVersionResource{Group: "node.kuberoot.dev", Version: "v1alpha1", Resource: "machines"}
)

// Controller runs the cluster's virtual machines through the node APIs of
// its nodes, and serves the machines' network its gateway.
type Controller struct {
	Dynamic dynamic.Interface
	Kube    kubernetes.Interface
	// Network of the machines; the gateway takes its first address.
	Network  netip.Prefix
	StateDir string

	dhcp *router.Daemon
	conf string
	nft  string
}

// Run reconciles every few seconds until ctx ends.
func (c *Controller) Run(ctx context.Context) error {
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		return err
	}
	c.dhcp = &router.Daemon{Name: "dnsmasq", Args: []string{"/usr/sbin/dnsmasq", "--conf-file=" + filepath.Join(c.StateDir, "dnsmasq.conf")}}
	defer c.dhcp.Stop()
	for {
		if err := c.gateway(); err != nil {
			klog.Errorf("gateway: %v", err)
		}
		c.reconcile(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *Controller) nodes(ctx context.Context) ([]Node, error) {
	list, err := c.Kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []Node
	for _, n := range list.Items {
		node := Node{Name: n.Name}
		for _, a := range n.Status.Addresses {
			if a.Type == corev1.NodeInternalIP {
				node.Address = a.Address
			}
		}
		for _, cond := range n.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				node.Ready = cond.Status == corev1.ConditionTrue
				if !node.Ready {
					node.DownSince = cond.LastTransitionTime.Time
				}
			}
		}
		out = append(out, node)
	}
	return out, nil
}

func (c *Controller) reconcile(ctx context.Context) {
	nodes, err := c.nodes(ctx)
	if err != nil {
		klog.Errorf("nodes: %v", err)
		return
	}
	list, err := c.Dynamic.Resource(vmsGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Errorf("virtual machines: %v", err)
		return
	}
	var vms []v1.VirtualMachine
	used, load := map[int32]bool{}, map[string]int{}
	for _, u := range list.Items {
		var m v1.VirtualMachine
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &m); err != nil {
			continue
		}
		vms = append(vms, m)
		if m.Status.Minor != 0 {
			used[m.Status.Minor] = true
		}
		if m.Status.Node != "" {
			load[m.Status.Node]++
		}
	}
	sort.Slice(vms, func(i, j int) bool { return vms[i].CreationTimestamp.Before(&vms[j].CreationTimestamp) })
	leases := c.leases()
	for i := range vms {
		m := &vms[i]
		if m.DeletionTimestamp != nil {
			c.cleanup(ctx, m, nodes)
			continue
		}
		if err := c.ensureFinalizer(ctx, m); err != nil {
			klog.Errorf("%s: %v", m.Name, err)
			continue
		}
		p := PlanFor(*m, nodes, used, load, time.Now())
		used[p.Minor] = true
		st := c.apply(ctx, m, p, nodes)
		st.Address = leases[strings.ToLower(p.MAC)]
		c.writeStatus(ctx, m, st)
	}
}

// apply makes the nodes run the plan: the disk's volume on each replica
// node, primary where the machine runs; the machine there and nowhere else.
func (c *Controller) apply(ctx context.Context, m *v1.VirtualMachine, p Plan, nodes []Node) v1.VirtualMachineStatus {
	st := v1.VirtualMachineStatus{Node: p.Node, ReplicaNodes: p.ReplicaNodes, MAC: p.MAC, Minor: p.Minor, Port: p.Port,
		Moves: m.Status.Moves, Phase: "Pending"}
	if p.Moved {
		st.Moves++
		klog.Infof("%s: node %s is down, starting it on %s", m.Name, m.Status.Node, p.Node)
	}
	if p.Waiting != "" {
		st.Message = p.Waiting
		if m.Status.Node != "" {
			st.Phase = "Moving"
		}
		return st
	}
	running := m.Spec.Running == nil || *m.Spec.Running
	byName := map[string]Node{}
	for _, n := range nodes {
		byName[n.Name] = n
	}
	// Machines first leave the nodes they no longer run on, so the volume
	// there can step down.
	for _, r := range p.ReplicaNodes {
		if r != p.Node && byName[r].Ready {
			_ = c.Dynamic.Resource(machinesGVR).Delete(ctx, r+"."+m.Name, metav1.DeleteOptions{})
		}
	}
	size := m.Spec.Disk.Size.Value()
	if size == 0 {
		size = 4 << 30
	}
	for i, r := range p.ReplicaNodes {
		n := byName[r]
		if !n.Ready {
			continue
		}
		spec := nodev1.VolumeSpec{SizeBytes: size, Minor: p.Minor, Port: p.Port, NodeID: int32(i),
			Primary: r == p.Node && running}
		if i == 0 {
			spec.Image = m.Spec.Disk.Image
		}
		for j, peer := range p.ReplicaNodes {
			if peer != r {
				spec.Peers = append(spec.Peers, nodev1.VolumePeer{Node: peer, Address: byName[peer].Address, NodeID: int32(j)})
			}
		}
		if err := c.ensure(ctx, volumesGVR, r+"."+m.Name, map[string]any{"spec": toMap(&spec)}); err != nil {
			st.Message = fmt.Sprintf("volume on %s: %v", r, err)
			return st
		}
	}
	vol, err := c.Dynamic.Resource(volumesGVR).Get(ctx, p.Node+"."+m.Name, metav1.GetOptions{})
	if err != nil {
		st.Message = err.Error()
		return st
	}
	role, _, _ := unstructured.NestedString(vol.Object, "status", "role")
	if msg, _, _ := unstructured.NestedString(vol.Object, "status", "message"); msg != "" {
		st.Message = "volume: " + msg
	}
	if running && role != "Primary" {
		st.Phase = "Starting"
		if st.Message == "" {
			st.Message = "waiting for the disk on " + p.Node
		}
		return st
	}
	mem := m.Spec.Memory.Value() >> 20
	if mem == 0 {
		mem = 512
	}
	cpus := m.Spec.CPUs
	if cpus == 0 {
		cpus = 1
	}
	machine := nodev1.MachineSpec{CPUs: cpus, MemoryMiB: mem, Volumes: []string{m.Name}, MAC: p.MAC, Running: running}
	if err := c.ensure(ctx, machinesGVR, p.Node+"."+m.Name, map[string]any{"spec": toMap(&machine)}); err != nil {
		st.Message = fmt.Sprintf("machine on %s: %v", p.Node, err)
		return st
	}
	got, err := c.Dynamic.Resource(machinesGVR).Get(ctx, p.Node+"."+m.Name, metav1.GetOptions{})
	if err != nil {
		st.Message = err.Error()
		return st
	}
	phase, _, _ := unstructured.NestedString(got.Object, "status", "phase")
	msg, _, _ := unstructured.NestedString(got.Object, "status", "message")
	st.Phase, st.Message = phase, msg
	if phase == "" {
		st.Phase = "Starting"
	}
	return st
}

// ensure creates a node object, or updates its spec when it differs.
func (c *Controller) ensure(ctx context.Context, gvr schema.GroupVersionResource, name string, want map[string]any) error {
	r := c.Dynamic.Resource(gvr)
	cur, err := r.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		kind := "Volume"
		if gvr.Resource == "machines" {
			kind = "Machine"
		}
		obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "node.kuberoot.dev/v1alpha1", "kind": kind,
			"metadata": map[string]any{"name": name}, "spec": want["spec"]}}
		_, err = r.Create(ctx, obj, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if spec, _, _ := unstructured.NestedMap(cur.Object, "spec"); reflect.DeepEqual(normalize(spec), normalize(want["spec"].(map[string]any))) {
		return nil
	}
	cur.Object["spec"] = want["spec"]
	_, err = r.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

// cleanup removes a deleted machine from every node, then lets it go.
func (c *Controller) cleanup(ctx context.Context, m *v1.VirtualMachine, nodes []Node) {
	ready := map[string]bool{}
	for _, n := range nodes {
		ready[n.Name] = n.Ready
	}
	for _, r := range m.Status.ReplicaNodes {
		if !ready[r] {
			klog.Infof("%s: node %s is down; its copy is removed when it is back", m.Name, r)
			continue
		}
		_ = c.Dynamic.Resource(machinesGVR).Delete(ctx, r+"."+m.Name, metav1.DeleteOptions{})
		if err := c.Dynamic.Resource(volumesGVR).Delete(ctx, r+"."+m.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			klog.Errorf("%s: volume on %s: %v", m.Name, r, err)
			return
		}
	}
	u, err := c.Dynamic.Resource(vmsGVR).Get(ctx, m.Name, metav1.GetOptions{})
	if err != nil {
		return
	}
	var keep []string
	for _, f := range u.GetFinalizers() {
		if f != finalizer {
			keep = append(keep, f)
		}
	}
	u.SetFinalizers(keep)
	_, _ = c.Dynamic.Resource(vmsGVR).Update(ctx, u, metav1.UpdateOptions{})
}

func (c *Controller) ensureFinalizer(ctx context.Context, m *v1.VirtualMachine) error {
	for _, f := range m.Finalizers {
		if f == finalizer {
			return nil
		}
	}
	u, err := c.Dynamic.Resource(vmsGVR).Get(ctx, m.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	u.SetFinalizers(append(u.GetFinalizers(), finalizer))
	_, err = c.Dynamic.Resource(vmsGVR).Update(ctx, u, metav1.UpdateOptions{})
	return err
}

func (c *Controller) writeStatus(ctx context.Context, m *v1.VirtualMachine, st v1.VirtualMachineStatus) {
	if reflect.DeepEqual(m.Status, st) {
		return
	}
	u, err := c.Dynamic.Resource(vmsGVR).Get(ctx, m.Name, metav1.GetOptions{})
	if err != nil {
		return
	}
	u.Object["status"] = toMap(&st)
	if _, err := c.Dynamic.Resource(vmsGVR).UpdateStatus(ctx, u, metav1.UpdateOptions{}); err != nil {
		klog.V(2).Infof("status of %s: %v", m.Name, err)
	}
}

// gateway gives the machines' network its gateway on this node: the
// network's first address on the bridge, NAT out of the node's other links,
// and DHCP and DNS.
func (c *Controller) gateway() error {
	if !c.Network.IsValid() {
		return nil
	}
	gw := c.Network.Masked().Addr().Next()
	addr := netip.PrefixFrom(gw, c.Network.Bits()).String()
	start, end := gw.Next().Next(), lastHost(c.Network)
	cfg := router.Config{
		Interfaces: []routerv1.Interface{{ObjectMeta: metav1.ObjectMeta{Name: "machines"}, Spec: routerv1.InterfaceSpec{Link: vm.Bridge, Addresses: []string{addr}}}},
		DHCP: []routerv1.DHCPServer{{ObjectMeta: metav1.ObjectMeta{Name: "machines"}, Spec: routerv1.DHCPServerSpec{
			Link: vm.Bridge, RangeStart: start.String(), RangeEnd: end.String(), LeaseTime: "12h"}}},
	}
	if out, err := uplink(); err == nil {
		cfg.NAT = []routerv1.NATRule{{ObjectMeta: metav1.ObjectMeta{Name: "machines"},
			Spec: routerv1.NATRuleSpec{Masquerade: &routerv1.Masquerade{OutLink: out, Sources: []string{c.Network.Masked().String()}}}}}
	}
	ok, problems := router.Check(cfg)
	if len(problems) > 0 {
		return fmt.Errorf("%v", problems)
	}
	for _, err := range router.ApplyInterfaces(ok.Interfaces) {
		return err // the bridge comes with the node's machines' network
	}
	if rs := router.Ruleset(ok); rs != c.nft {
		cmd := exec.Command("nft", "-f", "-")
		cmd.Stdin = strings.NewReader(rs)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("nft: %v: %s", err, out)
		}
		c.nft = rs
	}
	conf := router.DnsmasqConfig(ok, c.leaseFile()) + "user=root\npid-file=\n"
	if conf != c.conf || !c.dhcp.Running() {
		if err := os.WriteFile(filepath.Join(c.StateDir, "dnsmasq.conf"), []byte(conf), 0o644); err != nil {
			return err
		}
		c.dhcp.Restart()
		c.conf = conf
	}
	return nil
}

func (c *Controller) leaseFile() string { return filepath.Join(c.StateDir, "dnsmasq.leases") }

// leases maps the machines' MACs to the addresses they took.
func (c *Controller) leases() map[string]string {
	out := map[string]string{}
	f, err := os.Open(c.leaseFile())
	if err != nil {
		return out
	}
	defer f.Close()
	for _, l := range router.ParseLeases(f) {
		out[strings.ToLower(l.MAC)] = l.Address
	}
	return out
}

// uplink is the link of the default route.
func uplink() (string, error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return "", err
	}
	for _, r := range routes {
		if r.Dst == nil || r.Dst.IP.IsUnspecified() {
			l, err := netlink.LinkByIndex(r.LinkIndex)
			if err != nil {
				return "", err
			}
			return l.Attrs().Name, nil
		}
	}
	return "", fmt.Errorf("no default route")
}

func lastHost(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().As4()
	host := uint32(1)<<(32-p.Bits()) - 2
	v := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	v += host
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// toMap converts a pointer to a struct into its unstructured form.
func toMap(ptr any) map[string]any {
	m, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(ptr)
	return m
}

// normalize drops empty values, as the server does when it stores a spec.
func normalize(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		switch x := v.(type) {
		case nil:
		case string:
			if x != "" {
				out[k] = x
			}
		case bool:
			if x {
				out[k] = x
			}
		case int64:
			if x != 0 {
				out[k] = x
			}
		case []any:
			if len(x) > 0 {
				out[k] = fmt.Sprint(x)
			}
		default:
			out[k] = fmt.Sprint(x)
		}
	}
	return out
}
