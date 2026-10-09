# kuberoot

[Русская версия](README.ru.md)

kuberoot builds Kubernetes distributions that boot straight on hardware. Each one is a single image with its own Linux kernel, a small init that brings up Kubernetes, and a node that is managed through the Kubernetes API itself: there is no SSH, no shell and no separate node tool, `kubectl` reaches the node. Everything above the base is a [kubepkg](https://github.com/tym83/kubepkg) package, so a distribution is a base plus a meta package, the way a Linux distribution is a kernel plus packages.

The repository builds five distributions: `edge`, a general-purpose cluster for one to a few nodes; `router`, Kubernetes without containers that runs a network router; `hypervisor`, virtual machines on KVM with their disks replicated between nodes, moved off a node that fails; `ai`, language models served as processes of the nodes behind one OpenAI-compatible endpoint, with new versions rolled out and back by the cluster; and `rt`, a real-time controller whose pods get whole CPUs with no timer tick on a fully preemptible kernel.

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

## The ai distribution

`ai` serves language models with no pods and no container images: each node runs llama-server, built into the image as one static binary for the CPU, as a process of its own. A model is a resource of the cluster:

```yaml
apiVersion: ai.kuberoot.dev/v1alpha1
kind: Model
metadata: {name: qwen}
spec:
  replicas: 3
  contextSize: 4096
  parallel: 2
  source:
    url: https://example.org/models/qwen2.5-0.5b-instruct-q4_k_m.gguf
    sha256: 74a4da8c9fdbcd15bd1f6d01d621410d31c6fc00986f5eb687824e7b93d7a9db
```

`kuberoot-aictl`, on the control plane, places the model on as many nodes as it has replicas. Each node downloads the weights, uses them only if they match their hash, and starts the server. The control plane serves every model at one OpenAI-compatible endpoint, port 8000: `/v1/models`, and `/v1/chat/completions` and the other `/v1` calls, routed by the model the request names to its ready replicas in turn, with streamed answers passed through as they come. The endpoint has no authentication of its own yet: it is meant for a network the cluster trusts.

A new `spec.source` is a new version. It goes to one node first; once it is ready there and answers a trial request, the other nodes take it one at a time, each after the one before serves it, so the model never loses more than one replica to the change. A version that fails to start on the first node, is not ready there in fifteen minutes, or does not answer, is rolled back: every node stays on the version before, and `status.failedSHA256` keeps it from being tried again until `spec.source` changes. `status.previous` is the version before the current one; its weights stay on the nodes, and setting `spec.source` back to it rolls the model back at once.

Each node serves its part through the node API: `modelservers` (the models it serves, their phase and the weights they run) and `modelservers/log` (the server's output).

## The rt distribution

`rt` is a real-time controller: a control loop runs in a pod and wakes on time. The kernel is fully preemptible (PREEMPT_RT). CPUs 0 and 1 keep the node's own work, its interrupts and RCU callbacks; from CPU 2 on, CPUs run with no timer tick while one task runs on them. The kubelet hands whole CPUs out of those to Guaranteed pods that ask for whole CPUs, with their memory from the NUMA node of their CPUs, so the loop has its CPUs to itself:

```yaml
apiVersion: v1
kind: Pod
metadata: {name: loop}
spec:
  containers:
  - name: loop
    image: registry.example.org/plant/loop:1.4
    command: [chrt, --fifo, "80", /loop]
    securityContext:
      capabilities: {add: [SYS_NICE]}
    resources:
      requests: {cpu: "1", memory: 256Mi}
      limits: {cpu: "1", memory: 256Mi}
```

A task with CAP_SYS_NICE runs at a real-time priority (SCHED_FIFO); real-time tasks may take a CPU whole, and the kernel's fair server keeps the node's own work running beside them. SCHED_DEADLINE is not offered to pods: the kernel admits it only for tasks free to run on all the CPUs of their scheduling domain, and a pod's are pinned.

How late the node wakes a real-time task is a resource of the node API, `latencytests`: cyclictest runs a SCHED_FIFO thread on each CPU asked for, the CPUs without a timer tick by default, and the test reports per CPU and overall the least, mean and most latency and the 99th and 99.99th percentiles.

```yaml
apiVersion: node.kuberoot.dev/v1alpha1
kind: LatencyTest
metadata: {name: baseline}
spec: {durationSeconds: 300}
```

The distribution needs four CPUs or more. The kernel is built for it apart from the other distributions' (`distros/rt/kernel.config`); on amd64 its CPU layout is part of the kernel's own command line.

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
| `modelservers` | the language models the node serves (ai) |
| `latencytests` | the real-time latency the node delivers (rt) |

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
| `cmd/kuberoot-aictl` | the models controller and endpoint |
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
