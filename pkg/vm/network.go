package vm

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// The machines' network: a bridge on every node, joined between nodes by a
// VXLAN tunnel, so machines on different nodes share one segment and keep
// their addresses when they move.
const (
	vxlanLink = "vmvx0"
	vxlanID   = 4242
	vxlanPort = 4789
	// overhead of VXLAN in IPv4.
	overhead = 50
)

// EnsureNetwork keeps the bridge and the tunnel on the node's address, and
// floods the segment's broadcasts to every peer node.
func EnsureNetwork(self net.IP, peers []net.IP) error {
	parent, mtu, err := linkOf(self)
	if err != nil {
		return err
	}
	br, err := netlink.LinkByName(Bridge)
	if err != nil {
		if err := netlink.LinkAdd(&netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: Bridge, MTU: mtu - overhead}}); err != nil {
			return fmt.Errorf("bridge: %w", err)
		}
		if br, err = netlink.LinkByName(Bridge); err != nil {
			return err
		}
	}
	vx, err := netlink.LinkByName(vxlanLink)
	if v, ok := vx.(*netlink.Vxlan); err == nil && (!ok || v.VxlanId != vxlanID || !v.SrcAddr.Equal(self)) {
		_ = netlink.LinkDel(vx)
		err = fmt.Errorf("replaced")
	}
	if err != nil {
		link := &netlink.Vxlan{LinkAttrs: netlink.LinkAttrs{Name: vxlanLink, MTU: mtu - overhead},
			VxlanId: vxlanID, VtepDevIndex: parent, SrcAddr: self, Port: vxlanPort, Learning: true}
		if err := netlink.LinkAdd(link); err != nil {
			return fmt.Errorf("vxlan: %w", err)
		}
		if vx, err = netlink.LinkByName(vxlanLink); err != nil {
			return err
		}
	}
	if err := netlink.LinkSetMaster(vx, br); err != nil {
		return err
	}
	for _, l := range []netlink.Link{br, vx} {
		if err := netlink.LinkSetUp(l); err != nil {
			return err
		}
	}
	return syncFlood(vx.Attrs().Index, peers)
}

// syncFlood keeps one all-zero forwarding entry per peer: broadcasts and
// unknown destinations go to every node.
func syncFlood(index int, peers []net.IP) error {
	zero := net.HardwareAddr{0, 0, 0, 0, 0, 0}
	want := map[string]bool{}
	for _, p := range peers {
		want[p.String()] = true
		if err := netlink.NeighAppend(&netlink.Neigh{LinkIndex: index, Family: unix.AF_BRIDGE, State: netlink.NUD_PERMANENT,
			Flags: netlink.NTF_SELF, IP: p, HardwareAddr: zero}); err != nil && err != unix.EEXIST {
			return fmt.Errorf("flood to %s: %w", p, err)
		}
	}
	have, err := netlink.NeighList(index, unix.AF_BRIDGE)
	if err != nil {
		return nil
	}
	for _, n := range have {
		if n.HardwareAddr.String() == zero.String() && n.IP != nil && !want[n.IP.String()] {
			_ = netlink.NeighDel(&n)
		}
	}
	return nil
}

func linkOf(ip net.IP) (index, mtu int, err error) {
	links, err := netlink.LinkList()
	if err != nil {
		return 0, 0, err
	}
	for _, l := range links {
		addrs, _ := netlink.AddrList(l, netlink.FAMILY_V4)
		for _, a := range addrs {
			if a.IP.Equal(ip) {
				return l.Attrs().Index, l.Attrs().MTU, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("no link has %s", ip)
}
