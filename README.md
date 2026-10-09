# kuberoot

kuberoot builds Kubernetes distributions that boot straight on hardware. Each one is a single image with its own Linux kernel, a small init that brings up Kubernetes, and a node that is managed through the Kubernetes API itself: there is no SSH, no shell and no separate node tool, `kubectl` reaches the node. Everything above the base is a [kubepkg](https://github.com/tym83/kubepkg) package, so a distribution is a base plus a meta package, the way a Linux distribution is a kernel plus packages.

The repository builds one distribution so far, `edge`: a general-purpose cluster for one to a few nodes.

> Status: experimental. The `edge` distribution passes the Kubernetes conformance suite (462 of 462 on a three-node cluster, Kubernetes 1.37.1), but the API of the node and the layout of the state may still change.

## What a node is

- **Kernel**: Linux 6.18 built for the distribution, with every driver built in. The only modules a node loads are the ones its profile names (DRBD for replicated storage), signed with the build's key; after them, loading modules is switched off.
- **kinit**: PID 1. It mounts the read-only root and the state partition, brings up the network, issues the node's certificates and runs the services of the node's role: containerd, kubelet and kube-proxy everywhere, plus kine (the cluster store on SQLite), the API server, the controller manager and the scheduler on the control plane.
- **Node API**: every node serves `node.kuberoot.dev`, aggregated into the cluster API, so `kubectl get disks`, `kubectl get bootentries` or `kubectl create upgrade` act on a node. It works before a cluster exists, which is how a node is installed.
- **Pod network**: VXLAN between nodes, or plain routes on a flat network, or none, for a CNI package such as Cilium.
- **Packages**: the kubepkg operator runs on the control plane and installs the distribution's packages; the `edge` meta package and the platform packages live in [kubepkg-recipes](https://github.com/tym83/kubepkg-recipes).

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
| `cmd/mkimage`, `cmd/kuberoot-release` | boot media and signed releases |
| `pkg/` | the libraries behind them |
| `distros/edge` | the `edge` profile: services, modules, packages |
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
