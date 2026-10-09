#!/usr/bin/env bash
# Builds the kuberoot kernel. Runs in the builder (hack/hetzner.sh), natively for
# its architecture; sources and objects stay in $WORK between builds.
# Usage: scripts/build-kernel.sh <arch: amd64|arm64> [extra fragment...]
set -euo pipefail

ARCH=${1:?arch required}
shift
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=${WORK:-/work}
OUT=${KUBEROOT_OUT:-$ROOT/out}/$ARCH
KVER=$(cat "$ROOT/kernel/VERSION")

case "$ARCH" in
  amd64) KARCH=x86_64; IMAGE=arch/x86/boot/bzImage ;;
  arm64) KARCH=arm64; IMAGE=arch/arm64/boot/vmlinuz.efi ;;
  *) echo "unknown arch $ARCH" >&2; exit 1 ;;
esac

mkdir -p "$WORK/src" "$OUT"
cd "$WORK/src"
if [ ! -d "linux-$KVER" ]; then
  curl -fsSL "https://cdn.kernel.org/pub/linux/kernel/v${KVER%%.*}.x/linux-$KVER.tar.xz" | tar -xJ
fi
cd "linux-$KVER"
O="$WORK/build-$KARCH"
make -s ARCH=$KARCH O="$O" defconfig
frags="$ROOT/kernel/base.config"
for f in "$ARCH.config" "$@"; do [ -f "$ROOT/kernel/$f" ] && frags="$frags $ROOT/kernel/$f"; done
KCONFIG_CONFIG="$O/.config" scripts/kconfig/merge_config.sh -m -O "$O" "$O/.config" $frags >/dev/null
make -s ARCH=$KARCH O="$O" olddefconfig
# Modules are on only for out-of-tree drivers: everything defconfig would
# build as a module is built in, as it was with modules off.
sed -i 's/=m$/=y/' "$O/.config"
make -s ARCH=$KARCH O="$O" olddefconfig
make -s ARCH=$KARCH O="$O" -j"$(nproc)" "$(basename $IMAGE)" modules
grep -q "=m$" "$O/.config" && { echo "modules left in the kernel config:" >&2; grep "=m$" "$O/.config" >&2; exit 1; }
cp "$O/$IMAGE" "$OUT/vmlinuz.efi"
cp "$O/.config" "$OUT/kernel.config"
echo "kernel: $OUT/vmlinuz.efi ($(du -h "$OUT/vmlinuz.efi" | cut -f1))"
