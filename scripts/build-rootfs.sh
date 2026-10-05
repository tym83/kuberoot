#!/usr/bin/env bash
# Assembles the read-only node root filesystem as a squashfs image:
# upstream Kubernetes, containerd, runc, CNI plugins, kine, plus the few
# userspace tools they exec (nft, mount, conntrack). No shell, no package manager.
# Usage: scripts/build-rootfs.sh <arch: arm64|amd64>
set -euo pipefail

ARCH=${1:?arch required}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUT="$ROOT/out/$ARCH"
# shellcheck source=/dev/null
set -a; . "$ROOT/build/components.env"; set +a
VERSION=${KUBEROOT_VERSION:-0.1.0-dev}
DISTRO=${KUBEROOT_DISTRO:-edge}
[ -f "$ROOT/distros/$DISTRO/profile.yaml" ] || { echo "no distribution $DISTRO" >&2; exit 1; }

case "$ARCH" in
  arm64) PLATFORM=linux/arm64 ;;
  amd64) PLATFORM=linux/amd64 ;;
  *) echo "unknown arch $ARCH" >&2; exit 1 ;;
esac

mkdir -p "$OUT" "$ROOT/.cache/$ARCH"
"$ROOT/scripts/build-kubepkg.sh" "$ARCH"
for cmd in kinit kuberoot-node kuberoot-installer kuberoot-intents; do
  GOOS=linux GOARCH=$ARCH CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$OUT/$cmd" "$ROOT/cmd/$cmd"
done

# Upstream kubelet is linked against glibc; carry just its runtime next to musl.
rm -rf "$OUT/glibc" && mkdir -p "$OUT/glibc"
docker run --rm --platform "$PLATFORM" -v "$OUT/glibc:/glibc" "debian:trixie-slim" sh -euc '
  multiarch=$(ls -d /lib/*-linux-gnu | head -1)
  mkdir -p /glibc$multiarch /glibc/lib
  for l in libc.so.6 libresolv.so.2 libpthread.so.0; do cp -L $multiarch/$l /glibc$multiarch/; done
  loader=$(ls /lib/ld-linux-*.so.* /lib64/ld-linux-*.so.* 2>/dev/null | head -1)
  mkdir -p /glibc$(dirname $loader)
  cp -L $loader /glibc$loader
'

docker run --rm --platform "$PLATFORM" \
  -v "$OUT:/out" -v "$ROOT/.cache/$ARCH:/cache" -v "$ROOT/rootfs:/overlay:ro" \
  -v "$ROOT/distros/$DISTRO:/distro:ro" -e DISTRO="$DISTRO" \
  -e ARCH="$ARCH" -e VERSION="$VERSION" -e K8S_VERSION -e CONTAINERD_VERSION -e RUNC_VERSION -e CNI_VERSION -e KINE_VERSION \
  "alpine:$ALPINE_VERSION" sh -euo pipefail -c '
    apk add --no-cache -q curl squashfs-tools tar
    fetch() { [ -s "/cache/$2" ] || curl -fsSL -o "/cache/$2" "$1"; }

    r=/rootfs
    mkdir -p $r/etc/apk
    cp -r /etc/apk/keys $r/etc/apk/
    cp /etc/apk/repositories $r/etc/apk/
    apk add -q --root $r --initdb --no-scripts --no-cache \
      nftables mount umount conntrack-tools ca-certificates-bundle e2fsprogs
    rm -rf $r/etc/apk $r/lib/apk $r/var/cache/apk

    for b in kube-apiserver kube-controller-manager kube-scheduler kubelet kube-proxy kubectl; do
      fetch "https://dl.k8s.io/release/$K8S_VERSION/bin/linux/$ARCH/$b" "$b-$K8S_VERSION"
      install -D -m 0755 "/cache/$b-$K8S_VERSION" "$r/usr/bin/$b"
    done
    fetch "https://github.com/containerd/containerd/releases/download/v$CONTAINERD_VERSION/containerd-static-$CONTAINERD_VERSION-linux-$ARCH.tar.gz" "containerd-$CONTAINERD_VERSION.tgz"
    tar -xzf "/cache/containerd-$CONTAINERD_VERSION.tgz" -C $r/usr bin/containerd bin/containerd-shim-runc-v2 bin/ctr
    fetch "https://github.com/opencontainers/runc/releases/download/$RUNC_VERSION/runc.$ARCH" "runc-$RUNC_VERSION"
    install -m 0755 "/cache/runc-$RUNC_VERSION" $r/usr/bin/runc
    fetch "https://github.com/containernetworking/plugins/releases/download/$CNI_VERSION/cni-plugins-linux-$ARCH-$CNI_VERSION.tgz" "cni-$CNI_VERSION.tgz"
    mkdir -p $r/usr/libexec/cni
    tar -xzf "/cache/cni-$CNI_VERSION.tgz" -C $r/usr/libexec/cni ./bridge ./host-local ./loopback ./portmap
    fetch "https://github.com/k3s-io/kine/releases/download/$KINE_VERSION/kine-$ARCH" "kine-$KINE_VERSION"
    install -m 0755 "/cache/kine-$KINE_VERSION" $r/usr/bin/kine

    install -m 0755 /out/kinit $r/usr/sbin/kinit
    install -m 0755 /out/kuberoot-node $r/usr/bin/kuberoot-node
    install -m 0755 /out/kuberoot-installer /out/kuberoot-intents $r/usr/bin/
    install -m 0755 /out/kubepkg /out/kubepkg-operator $r/usr/bin/
    mkdir -p $r/usr/share/kuberoot/kubepkg/crds
    cp /out/kubepkg-crds/*.yaml $r/usr/share/kuberoot/kubepkg/crds/
    cp -r /out/glibc/. $r/
    cp -r /overlay/. $r/
    install -m 0644 /distro/profile.yaml $r/usr/share/kuberoot/profile.yaml
    printf "NAME=\"kuberoot\"\nID=kuberoot\nPRETTY_NAME=\"kuberoot %s (%s)\"\nVERSION_ID=%s\nHOME_URL=\"https://github.com/tym83/kuberoot\"\n" "$VERSION" "$DISTRO" "$VERSION" > $r/etc/os-release
    mkdir -p $r/dev $r/proc $r/sys $r/run $r/tmp $r/var $r/etc/kubernetes $r/etc/cni/net.d
    rm -f /out/rootfs.squashfs
    mksquashfs $r /out/rootfs.squashfs -comp zstd -all-root -noappend -quiet
  '
echo "$VERSION" > "$OUT/VERSION"
echo "rootfs: out/$ARCH/rootfs.squashfs ($(du -h "$OUT/rootfs.squashfs" | cut -f1))"
