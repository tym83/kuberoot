#!/usr/bin/env bash
# Boots like real hardware: UEFI firmware, boot media on USB, an empty target disk.
# Usage: hack/qemu-uefi.sh <arch> [--media out/<arch>/kuberoot-media.img] [--disk out/<arch>/disk.raw]
set -euo pipefail

ARCH=${1:?arch required}
shift
OUT="$(cd "$(dirname "$0")/.." && pwd)/out/$ARCH"
MEDIA="" DISK="$OUT/disk.raw"
while [ $# -gt 0 ]; do
  case "$1" in
    --media) MEDIA=$2; shift 2 ;;
    --disk) DISK=$2; shift 2 ;;
    *) break ;;
  esac
done
[ -f "$DISK" ] || truncate -s 8G "$DISK"

case "$ARCH" in
  arm64)
    accel=tcg; [ "$(uname -sm)" = "Darwin arm64" ] && accel=hvf
    machine=(qemu-system-aarch64 -M virt -accel "$accel" -cpu "$([ $accel = hvf ] && echo host || echo max)"
      -bios /opt/homebrew/share/qemu/edk2-aarch64-code.fd) ;;
  amd64)
    machine=(qemu-system-x86_64 -M q35 -cpu max -bios /opt/homebrew/share/qemu/edk2-x86_64-code.fd) ;;
esac

media=()
if [ -n "$MEDIA" ]; then
  media=(-device qemu-xhci -drive "if=none,id=usb,format=raw,file=$MEDIA" -device usb-storage,drive=usb,bootindex=0)
fi

# Upgrade tests need real reboots; installs exit on reboot so the media can be pulled.
noreboot=-no-reboot
[ -n "${REBOOT:-}" ] && noreboot=

exec "${machine[@]}" -smp 4 -m 4096 -nographic $noreboot \
  -drive "if=none,id=disk,format=raw,file=$DISK" -device virtio-blk-pci,drive=disk,bootindex=1 \
  ${media[@]+"${media[@]}"} \
  -netdev user,id=n0,hostfwd=tcp::6443-:6443,hostfwd=tcp::50000-:50000 \
  -device virtio-net-pci,netdev=n0 -device virtio-rng-pci \
  "$@"
