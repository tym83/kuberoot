#!/usr/bin/env bash
# Boots N development nodes (direct kernel boot) on a shared cluster network.
# Node i: eth0 user-mode NAT for the internet, eth1 on 192.168.100.1<i>/24.
# Node 1 forwards 6443 and 50000; node i forwards 5000<i> to its node API.
# Console logs: out-node<i>.log. Usage: hack/qemu-cluster.sh <arch> <nodes>
set -euo pipefail

ARCH=${1:?arch required}
NODES=${2:-2}
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/out/$ARCH"

for i in $(seq 1 "$NODES"); do
  fwd="hostfwd=tcp::5000$i-:50000"
  [ "$i" = 1 ] && fwd="hostfwd=tcp::6443-:6443,hostfwd=tcp::50000-:50000,$fwd"
  # Node 1 listens on the cluster link, the others connect to it.
  link="connect=127.0.0.1:5560"
  [ "$i" = 1 ] && link="listen=127.0.0.1:5560"
  qemu-system-aarch64 -M virt -accel hvf -cpu host -smp 2 -m 3072 -nographic -no-reboot \
    -kernel "$OUT/kernel" -initrd "$OUT/initramfs.cpio" \
    -append "console=ttyAMA0 panic=-1 loglevel=4 kuberoot.dev kuberoot.nameserver=1.1.1.1 kuberoot.ip=eth1:192.168.100.1$i/24 ${EXTRA_ARGS:-}" \
    -netdev "user,id=n0,$fwd" -device "virtio-net-pci,netdev=n0,mac=52:54:00:00:01:0$i" \
    -netdev "socket,id=n1,$link" -device "virtio-net-pci,netdev=n1,mac=52:54:00:00:02:0$i" \
    -device virtio-rng-pci > "$ROOT/out-node$i.log" 2>&1 &
  [ "$i" = 1 ] && sleep 1
done
echo "started $NODES nodes"
