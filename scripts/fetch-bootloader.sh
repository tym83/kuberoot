#!/usr/bin/env bash
# Extracts the systemd-boot EFI binary from Debian's systemd-boot-efi package.
# Usage: scripts/fetch-bootloader.sh <arch: arm64|amd64>
set -euo pipefail

ARCH=${1:?arch required}
OUT="$(cd "$(dirname "$0")/.." && pwd)/out/$ARCH"
case "$ARCH" in
  arm64) PLATFORM=linux/arm64; EFI=systemd-bootaa64.efi ;;
  amd64) PLATFORM=linux/amd64; EFI=systemd-bootx64.efi ;;
  *) echo "unknown arch $ARCH" >&2; exit 1 ;;
esac

mkdir -p "$OUT"
docker run --rm --platform "$PLATFORM" -v "$OUT:/out" -e EFI="$EFI" debian:trixie-slim sh -euc '
  apt-get -qq update
  cd /tmp && apt-get -qq download systemd-boot-efi
  dpkg-deb -x systemd-boot-efi_*.deb x
  cp "x/usr/lib/systemd/boot/efi/$EFI" /out/systemd-boot.efi
  dpkg-deb -f systemd-boot-efi_*.deb Version > /out/systemd-boot.version
'
echo "bootloader: out/$ARCH/systemd-boot.efi ($(cat "$OUT/systemd-boot.version"))"
