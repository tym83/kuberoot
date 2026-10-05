#!/usr/bin/env bash
# Builds the pinned kubepkg operator and CLI for the target and collects its CRDs.
# Usage: scripts/build-kubepkg.sh <arch: arm64|amd64>
set -euo pipefail

ARCH=${1:?arch required}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUT="$ROOT/out/$ARCH"
mkdir -p "$OUT/kubepkg-crds"

cd "$ROOT/build/kubepkg"
go mod tidy >/dev/null
for cmd in kubepkg kubepkg-operator; do
  GOOS=linux GOARCH=$ARCH CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$OUT/$cmd" "github.com/tym83/kubepkg/cmd/$cmd"
done
src=$(go list -m -f '{{.Dir}}' github.com/tym83/kubepkg)
install -m 0644 "$src"/config/crd/kubepkg.dev_*.yaml "$OUT/kubepkg-crds/"
echo "kubepkg: $(go list -m -f '{{.Version}}' github.com/tym83/kubepkg) -> out/$ARCH/{kubepkg,kubepkg-operator,kubepkg-crds}"
