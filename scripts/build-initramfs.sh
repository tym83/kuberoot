#!/usr/bin/env bash
# Packs the boot stage: kinit as /init plus the root filesystem image it mounts.
# Usage: scripts/build-initramfs.sh <arch: arm64|amd64>
set -euo pipefail

ARCH=${1:?arch required}
OUT="$(cd "$(dirname "$0")/.." && pwd)/out/$ARCH"

docker build -q -t kuberoot-builder -f "$OUT/../../build/Dockerfile.builder" "$OUT/../../build" >/dev/null
docker run --rm -v "$OUT:/out" kuberoot-builder bash -euo pipefail -c '
  stage=$(mktemp -d)
  cd "$stage"
  mkdir -p dev proc sys newroot
  install -m 0755 /out/kinit init
  cp /out/rootfs.squashfs rootfs.squashfs
  # Left uncompressed: the squashfs inside is already zstd-compressed.
  find . -print0 | sort -z | cpio --null -o -H newc -R 0:0 --quiet > /out/initramfs.cpio
'
echo "initramfs: out/$ARCH/initramfs.cpio ($(du -h "$OUT/initramfs.cpio" | cut -f1))"
