#!/usr/bin/env bash
# Packs the current build into a release bundle that the node API Upgrade resource installs.
# Usage: scripts/build-bundle.sh <arch: arm64|amd64>
set -euo pipefail

ARCH=${1:?arch required}
OUT="$(cd "$(dirname "$0")/.." && pwd)/out/$ARCH"
VERSION=$(cat "$OUT/VERSION")
BUNDLE="$OUT/bundles/kuberoot-$VERSION-$ARCH.tar"

mkdir -p "$OUT/bundles"
tar -C "$OUT" -cf "$BUNDLE" VERSION vmlinuz.efi initrd.cpio rootfs.squashfs
shasum -a 256 "$BUNDLE" | cut -d' ' -f1 > "$BUNDLE.sha256"
# Nodes install only bundles signed with a key their image trusts.
(cd "$OUT/../.." && go run ./cmd/kuberoot-release sign "$BUNDLE" "${KUBEROOT_RELEASE_KEY:-.cache/release.key}" >/dev/null)
echo "bundle: ${BUNDLE#$OUT/} ($(du -h "$BUNDLE" | cut -f1)), signed, sha256 $(cat "$BUNDLE.sha256")"
