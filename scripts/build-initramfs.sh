#!/usr/bin/env bash
# Packs kinit and the node root filesystem into a gzip'd newc initramfs.
# Usage: scripts/build-initramfs.sh <arch: arm64|amd64>
set -euo pipefail

ARCH=${1:?arch required}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUT="$ROOT/out/$ARCH"

mkdir -p "$OUT"
GOOS=linux GOARCH=$ARCH CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$OUT/kinit" "$ROOT/cmd/kinit"

docker run --rm -v "$OUT:/out" kuberoot-builder bash -euo pipefail -c '
  rootfs=$(mktemp -d)
  cd "$rootfs"
  mkdir -p dev proc sys run tmp etc var/lib usr/bin
  install -m 0755 /out/kinit init
  echo kuberoot > etc/hostname
  find . -print0 | sort -z | cpio --null -o -H newc -R 0:0 --quiet | gzip -9 > /out/initramfs.cpio.gz
'
echo "initramfs: out/$ARCH/initramfs.cpio.gz ($(du -h "$OUT/initramfs.cpio.gz" | cut -f1))"
