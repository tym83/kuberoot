# kuberoot

[Русская версия](README.ru.md)

kuberoot builds Kubernetes distributions that boot straight on hardware. Each one is a single image with its own Linux kernel, a small init that brings up Kubernetes, and a node that is managed through the Kubernetes API itself: there is no SSH, no shell and no separate node tool, `kubectl` reaches the node. Everything above the base is a [kubepkg](https://github.com/tym83/kubepkg) package, so a distribution is a base plus a meta package, the way a Linux distribution is a kernel plus packages.

The repository builds three distributions: `edge`, a general-purpose cluster for one to a few nodes; `router`, Kubernetes without containers that runs a network router; and `hypervisor`, virtual machines on KVM with their disks replicated between nodes, moved off a node that fails.

> Status: experimental. The `edge` distribution passes the Kubernetes conformance suite (462 of 462 on a three-node cluster, Kubernetes 1.37.1), but the API of the node and the layout of the state may still change.

## What a node is

- **Kernel**: Linux 6.18 built for the distribution, with every driver built in. The only modules a node loads are the ones its profile names (DRBD for replicated storage), signed with the build's key; after them, loading modules is switched off.
- **kinit**: PID 1. It mounts the read-only root and the state partition, brings up the network, issues the node's certificates and runs the services of the node's role: containerd, kubelet and kube-proxy everywhere, plus kine (the cluster store on SQLite), the API server, the controller manager and the scheduler on the control plane.
- **Node API**: every node serves `node.kuberoot.dev`, aggregated into the cluster API, so `kubectl get disks`, `kubectl get bootentries` or `kubectl create upgrade` act on a node. It works before a cluster exists, which is how a node is installed.
- **Pod network**: VXLAN between nodes, or plain routes on a flat network, or none, for a CNI package such as Cilium.
- **Packages**: the kubepkg operator runs on the control plane and installs the distribution's packages; the `edge` meta package and the platform packages live in [kubepkg-recipes](https://github.com/tym83/kubepkg-recipes).

## The router distribution

`router` keeps only the part of Kubernetes that holds and watches configuration: kine, the API server, a controller manager for garbage collection, and the node API. There is no kubelet, container runtime, kube-proxy or scheduler. The router's configuration is resources of `router.kuberoot.dev`, with RBAC, audit, watch and GitOps as for any resource, and `kuberoot-router` makes the node match them:

| Resource | What it configures |
|---|---|
| `Interface` | a link's addresses and MTU, or a VLAN on a parent link |
| `Route` | a static route |
| `NATRule` | masquerade behind an outgoing link, or a port forwarded to a host |
| `FirewallZone`, `FirewallRule` | zones of links, what may reach the router and what may cross between zones |
| `DHCPServer` | DHCP on a link, and a DNS cache for it |
| `BGPRouter`, `BGPPeer` | the router's AS and announced networks, and its BGP sessions |
| `Safeguard` | changes on trial, undone unless confirmed in time |

A home gateway, with a LAN on `eth1` behind the uplink `eth0`:

```yaml
apiVersion: router.kuberoot.dev/v1alpha1
kind: Interface
metadata: {name: lan}
spec: {link: eth1, addresses: [192.168.10.1/24]}
---
apiVersion: router.kuberoot.dev/v1alpha1
kind: NATRule
metadata: {name: out}
spec: {masquerade: {outLink: eth0, sources: [192.168.10.0/24]}}
---
apiVersion: router.kuberoot.dev/v1alpha1
kind: DHCPServer
metadata: {name: lan}
spec: {link: eth1, rangeStart: 192.168.10.100, rangeEnd: 192.168.10.199}
---
apiVersion: router.kuberoot.dev/v1alpha1
kind: FirewallZone
metadata: {name: wan}
spec: {links: [eth0], input: Drop, management: true}
---
apiVersion: router.kuberoot.dev/v1alpha1
kind: FirewallZone
metadata: {name: lan}
spec: {links: [eth1], input: Accept, forwardTo: [wan]}
```

Every resource reports in its status whether the node runs it and, if not, why; interfaces report their state, DHCP servers their leases, BGP peers their sessions. The firewall filters only once a zone exists, and the router's API stays open on management zones whatever the rules say. With a `Safeguard`, every change runs on trial: unless `spec.confirm` names its revision in time, the router goes back to the last confirmed configuration, so a change that cuts the administrators off undoes itself.

## The hypervisor distribution

`hypervisor` runs virtual machines on KVM with cloud-hypervisor: no libvirt, and no pods. A machine is a resource of the cluster:

```yaml
apiVersion: vm.kuberoot.dev/v1alpha1
kind: VirtualMachine
metadata: {name: web}
spec:
  cpus: 2
  memory: 2Gi
  replicas: 3
  disk: {size: 20Gi, image: "https://example.org/images/debian-13-uefi.qcow2"}
```

`kuberoot-vmctl`, on the control plane, places the machine's disk on as many nodes as it has replicas and the machine on one of them. The disk is a DRBD volume: every write reaches the other nodes before it is done, and a node cut off from the majority stops writing. When the machine's node has been down for half a minute, the machine starts on another node holding its disk, with the same disk and address; the node that comes back catches up and drops its copy of the machine. Machines share one network across the nodes (a bridge on each, joined by VXLAN), with a gateway on the control plane for DHCP and the way out.

A running machine moves to another node alive when its `spec.node` changes to another of its replica nodes: the disk is writable on both for the moment of the move, the machine's memory is copied over while it runs, and it goes on there with the same disk and address, without a reboot. A move that does not finish in ten minutes, or loses a node on the way, is called off and the machine stays where it was; `status.failedMigration` names the node until `spec.node` changes again.

Each node serves its part through the node API: `volumes` (the disks on it, with their DRBD role and state), `machines` (the machines it runs) and `machines/console` (a machine's serial console). The kubelet stays for what the cluster knows of its nodes, their registration and heartbeat; no pods run.

## Node API

| Resource | What it does |
|---|---|
| `osconfigs` | the node's operating system settings |
| `nodeservices` | the services kinit runs, their state and logs |
| `disks` | block devices |
| `installations` | writes the system onto a disk, from boot media |
| `bootentries` | the two root slots and which one boots |
| `upgrades` | stages a signed release into the inactive slot |
| `memberships`, `jointickets` | joins nodes to a cluster |
| `statebackups` | the control plane's state archives |
| `volumes`, `machines` | virtual machines' disks and machines (hypervisor) |

## Installing and upgrading

A node boots from media (`mkimage` writes a disk image with the kernel, the initramfs and the root filesystem). On the live system, an `Installation` partitions a disk: an EFI partition with systemd-boot, two root slots and a state partition. The node's identity, its membership of a cluster and, on a control plane, a snapshot of the cluster store move onto the disk with it.

An `Upgrade` downloads a signed release bundle, writes it to the inactive slot and boots it on probation: unless every service runs without restarting and the node is Ready within five minutes, the node reboots, and once the attempts run out it boots the previous slot again.

## Backups of the control plane

A control plane keeps its state in one place: the cluster store, its certificate authorities and its identity. The node API archives it every hour, and on request with `kubectl create statebackup`, keeps the latest twelve locally and, when the Secret `kube-system/kuberoot-state-backup` says so, uploads them to S3-compatible storage:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: kuberoot-state-backup
  namespace: kube-system
stringData:
  interval: 1h
  keep: "12"
  encryptionRecipient: age1...        # archives are encrypted to this age key
  s3Endpoint: https://s3.example.org
  s3Bucket: backups
  s3AccessKey: ...
  s3SecretKey: ...
```

Archives hold the cluster's keys and every Secret, so they leave the node only encrypted (`age-keygen` makes a key pair; the secret key stays with you). To bring a lost control plane back on new hardware, boot the new machine from media and install it from a backup:

```yaml
apiVersion: node.kuberoot.dev/v1alpha1
kind: Installation
metadata:
  name: restore
spec:
  disk: vda
  reboot: true
  restore:
    url: https://...      # a presigned link to the archive
    sha256: ...           # as the StateBackup reports it
    identity: AGE-SECRET-KEY-1...
```

The node comes back as the same node, with the same name, certificates and cluster.

## Layout

| Path | |
|---|---|
| `cmd/kinit` | PID 1 |
| `cmd/kuberoot-node` | the node API server |
| `cmd/kuberoot-installer` | the installer's console interface |
| `cmd/kuberoot-intents` | lowers typed intents to Kubernetes primitives |
| `cmd/kuberoot-router` | the router controller |
| `cmd/kuberoot-vmctl` | the virtual machines controller |
| `cmd/mkimage`, `cmd/kuberoot-release` | boot media and signed releases |
| `pkg/` | the libraries behind them |
| `distros/` | the distributions: services, modules, add-ons, packages |
| `kernel/` | kernel configuration and versions |
| `rootfs/` | files of the root filesystem |
| `scripts/` | the build steps |

## Building

Builds run in a container with the toolchain, not on the workstation: `scripts/` hold the steps (kernel, DRBD, binaries, root filesystem, initramfs, bundle). `hack/hetzner.sh` drives a builder pod in a Kubernetes cluster and runs them there; `hack/hetzner/` has the builder, a registry and the VM definitions the author tests nodes with.

```sh
hack/hetzner.sh build amd64      # kernel if missing, binaries, root filesystem, initramfs
hack/hetzner.sh bundle amd64     # a signed release bundle
hack/hetzner.sh media amd64      # boot media
hack/hetzner.sh test             # go test ./...
```

## License

Apache License 2.0, see [LICENSE](LICENSE).
