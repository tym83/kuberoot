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
)

// setupNetwork brings up loopback and configures the first ethernet link via DHCP.
func setupNetwork() error {
	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("loopback: %w", err)
	}
	if err := netlink.LinkSetUp(lo); err != nil {
		return fmt.Errorf("loopback up: %w", err)
	}
	link, err := firstEthernet()
	if err != nil {
		return err
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("%s up: %w", link.Attrs().Name, err)
	}
	return dhcp(link)
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

func dhcp(link netlink.Link) error {
	name := link.Attrs().Name
	client, err := nclient4.New(name)
	if err != nil {
		return fmt.Errorf("dhcp client on %s: %w", name, err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lease, err := client.Request(ctx)
	if err != nil {
		return fmt.Errorf("dhcp on %s: %w", name, err)
	}
	ack := lease.ACK
	ipnet := &net.IPNet{IP: ack.YourIPAddr, Mask: ack.SubnetMask()}
	if err := netlink.AddrReplace(link, &netlink.Addr{IPNet: ipnet}); err != nil {
		return fmt.Errorf("address %s: %w", ipnet, err)
	}
	if routers := ack.Router(); len(routers) > 0 {
		route := &netlink.Route{LinkIndex: link.Attrs().Index, Gw: routers[0]}
		if err := netlink.RouteReplace(route); err != nil {
			return fmt.Errorf("default route: %w", err)
		}
	}
	var resolv strings.Builder
	for _, dns := range ack.DNS() {
		fmt.Fprintf(&resolv, "nameserver %s\n", dns)
	}
	if err := os.WriteFile("/etc/resolv.conf", []byte(resolv.String()), 0o644); err != nil {
		return fmt.Errorf("resolv.conf: %w", err)
	}
	log.Printf("%s: %s via %v", name, ipnet, ack.Router())
	return nil
}
