#!/usr/bin/env bash
# Packs the boot stage: kinit as /init. initrd.cpio is for disks and boot media,
# where the root comes from a partition; initramfs.cpio carries the root image
# too, for booting a kernel directly. Runs in the builder.
# Usage: scripts/build-initramfs.sh <arch: amd64|arm64>
set -euo pipefail

ARCH=${1:?arch required}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUT=${KUBEROOT_OUT:-$ROOT/out}/$ARCH
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
cd "$stage"
mkdir -p dev proc sys newroot
install -m 0755 "$OUT/kinit" init
find . -print0 | sort -z | cpio --null -o -H newc -R 0:0 --quiet > "$OUT/initrd.cpio"
# Left uncompressed: the squashfs inside is already zstd-compressed.
cp "$OUT/rootfs.squashfs" rootfs.squashfs
find . -print0 | sort -z | cpio --null -o -H newc -R 0:0 --quiet > "$OUT/initramfs.cpio"
echo "initrd: $OUT/initrd.cpio"
