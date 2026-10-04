#!/usr/bin/env bash
# Builds the kuberoot kernel inside the builder container.
# Usage: scripts/build-kernel.sh <arch: arm64|amd64> [extra fragment...]
set -euo pipefail

ARCH=${1:?arch required}
shift
KVER=$(cat kernel/VERSION)
ROOT=$(cd "$(dirname "$0")/.." && pwd)

case "$ARCH" in
  arm64) KARCH=arm64; IMAGE=arch/arm64/boot/Image; CROSS= ;;
  amd64) KARCH=x86_64; IMAGE=arch/x86/boot/bzImage; CROSS=x86_64-linux-gnu- ;;
  *) echo "unknown arch $ARCH" >&2; exit 1 ;;
esac

docker build -q -t kuberoot-builder -f "$ROOT/build/Dockerfile.builder" "$ROOT/build" >/dev/null
mkdir -p "$ROOT/out/$ARCH"

# Sources and objects live in a docker volume: the macOS bind mount is too slow for a kernel build.
docker run --rm \
  -v kuberoot-kernel:/work \
  -v "$ROOT/kernel:/cfg:ro" \
  -v "$ROOT/out/$ARCH:/out" \
  -e KVER="$KVER" -e KARCH="$KARCH" -e IMAGE="$IMAGE" -e CROSS_COMPILE="$CROSS" \
  -e FRAGMENTS="$*" \
  kuberoot-builder bash -euo pipefail -c '
    cd /work
    if [ ! -d "linux-$KVER" ]; then
      curl -fsSL "https://cdn.kernel.org/pub/linux/kernel/v${KVER%%.*}.x/linux-$KVER.tar.xz" | tar -xJ
    fi
    cd "linux-$KVER"
    O="/work/build-$KARCH"
    make -s ARCH=$KARCH O=$O defconfig
    frags=/cfg/base.config
    for f in $FRAGMENTS; do frags="$frags /cfg/$f"; done
    KCONFIG_CONFIG=$O/.config scripts/kconfig/merge_config.sh -m -O $O $O/.config $frags >/dev/null
    make -s ARCH=$KARCH O=$O olddefconfig
    make -s ARCH=$KARCH O=$O -j"$(nproc)" "$(basename $IMAGE)"
    cp "$O/$IMAGE" /out/kernel
    cp "$O/.config" /out/kernel.config
  '
echo "kernel: out/$ARCH/kernel"
