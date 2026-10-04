package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// nodeInfo is what the rest of the boot needs to know about this machine.
type nodeInfo struct {
	name string
	ip   net.IP
}

// setupNetwork brings up loopback and configures the first ethernet link via DHCP.
func setupNetwork(cfg bootConfig) (nodeInfo, error) {
	var node nodeInfo
	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return node, fmt.Errorf("loopback: %w", err)
	}
	if err := netlink.LinkSetUp(lo); err != nil {
		return node, fmt.Errorf("loopback up: %w", err)
	}
	link, err := firstEthernet()
	if err != nil {
		return node, err
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return node, fmt.Errorf("%s up: %w", link.Attrs().Name, err)
	}
	mac := link.Attrs().HardwareAddr
	node.name = fmt.Sprintf("kuberoot-%02x%02x%02x", mac[3], mac[4], mac[5])
	node.ip, err = dhcp(link, cfg.nameservers)
	return node, err
}

func setHostname(node nodeInfo) error {
	if err := unix.Sethostname([]byte(node.name)); err != nil {
		return fmt.Errorf("hostname: %w", err)
	}
	hosts := fmt.Sprintf("127.0.0.1 localhost\n::1 localhost\n%s %s\n", node.ip, node.name)
	if err := os.WriteFile("/etc/hosts", []byte(hosts), 0o644); err != nil {
		return err
	}
	return os.WriteFile("/etc/hostname", []byte(node.name+"\n"), 0o644)
}

func firstEthernet() (netlink.Link, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		if l.Type() == "device" && l.Attrs().Name != "lo" && len(l.Attrs().HardwareAddr) == 6 {
			return l, nil
		}
	}
	return nil, fmt.Errorf("no ethernet link found")
}

func dhcp(link netlink.Link, nameservers []string) (net.IP, error) {
	name := link.Attrs().Name
	client, err := nclient4.New(name)
	if err != nil {
		return nil, fmt.Errorf("dhcp client on %s: %w", name, err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lease, err := client.Request(ctx)
	if err != nil {
		return nil, fmt.Errorf("dhcp on %s: %w", name, err)
	}
	ack := lease.ACK
	ipnet := &net.IPNet{IP: ack.YourIPAddr, Mask: ack.SubnetMask()}
	if err := netlink.AddrReplace(link, &netlink.Addr{IPNet: ipnet}); err != nil {
		return nil, fmt.Errorf("address %s: %w", ipnet, err)
	}
	if routers := ack.Router(); len(routers) > 0 {
		route := &netlink.Route{LinkIndex: link.Attrs().Index, Gw: routers[0]}
		if err := netlink.RouteReplace(route); err != nil {
			return nil, fmt.Errorf("default route: %w", err)
		}
	}
	if len(nameservers) == 0 {
		for _, dns := range ack.DNS() {
			nameservers = append(nameservers, dns.String())
		}
	}
	var resolv strings.Builder
	for _, dns := range nameservers {
		fmt.Fprintf(&resolv, "nameserver %s\n", dns)
	}
	if err := os.WriteFile("/etc/resolv.conf", []byte(resolv.String()), 0o644); err != nil {
		return nil, fmt.Errorf("resolv.conf: %w", err)
	}
	log.Printf("%s: %s via %v", name, ipnet, ack.Router())
	return ack.YourIPAddr, nil
}
