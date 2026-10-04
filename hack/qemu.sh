#!/usr/bin/env bash
# Boots a kuberoot build in QEMU with the serial console on stdout.
# Usage: hack/qemu.sh <arch: arm64|amd64> [extra qemu args...]
set -euo pipefail

ARCH=${1:?arch required}
shift
OUT="$(cd "$(dirname "$0")/.." && pwd)/out/$ARCH"

case "$ARCH" in
  arm64)
    accel=tcg; [ "$(uname -sm)" = "Darwin arm64" ] && accel=hvf
    machine=(qemu-system-aarch64 -M virt -accel "$accel" -cpu "$([ $accel = hvf ] && echo host || echo max)")
    console=ttyAMA0 ;;
  amd64)
    machine=(qemu-system-x86_64 -M q35 -cpu max)
    console=ttyS0 ;;
esac

# QEMU's DNS proxy cannot use the IPv6 link-local resolvers macOS often lists first,
# and home routers tend to refuse queries arriving through user-mode NAT.
dns=${DNS:-1.1.1.1}

exec "${machine[@]}" -smp 4 -m 4096 -nographic -no-reboot \
  -kernel "$OUT/kernel" -initrd "$OUT/initramfs.cpio" \
  -append "console=$console panic=-1 ${dns:+kuberoot.nameserver=$dns} ${KAPPEND:-quiet}" \
  -netdev user,id=n0,hostfwd=tcp::6443-:6443,hostfwd=tcp::50000-:50000 \
  -device virtio-net-pci,netdev=n0 \
  "$@"
