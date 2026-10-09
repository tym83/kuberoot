#!/usr/bin/env bash
# Builds llama-server, the ai distribution's model server, as one static
# binary for the CPU: no shared libraries, no GPU backends. Runs in the builder.
# Usage: scripts/build-llama.sh <arch: amd64|arm64>
set -euo pipefail

ARCH=${1:?arch required}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=${WORK:-/work}
OUT=${KUBEROOT_OUT:-$ROOT/out}/$ARCH
. "$ROOT/build/components.env"
bin="$OUT/llama-server-$LLAMA_CPP_VERSION"
[ -f "$bin" ] && { echo "llama-server $LLAMA_CPP_VERSION: $bin (built before)"; exit 0; }

cross=()
case "$ARCH" in
  amd64) pkgs=(cmake g++) ;;
  arm64)
    pkgs=(cmake g++-aarch64-linux-gnu)
    cross=(-DCMAKE_SYSTEM_NAME=Linux -DCMAKE_SYSTEM_PROCESSOR=aarch64
      -DCMAKE_C_COMPILER=aarch64-linux-gnu-gcc -DCMAKE_CXX_COMPILER=aarch64-linux-gnu-g++) ;;
  *) echo "unknown arch $ARCH" >&2; exit 1 ;;
esac
missing=()
for p in "${pkgs[@]}"; do dpkg -s "$p" >/dev/null 2>&1 || missing+=("$p"); done
[ ${#missing[@]} -eq 0 ] || { apt-get -qq update && apt-get -qq install -y "${missing[@]}" >/dev/null; }

src="$WORK/src/llama.cpp-$LLAMA_CPP_VERSION"
[ -d "$src" ] || git clone -q --depth 1 --branch "$LLAMA_CPP_VERSION" https://github.com/ggml-org/llama.cpp "$src"
b="$WORK/build-llama-$ARCH"
# OpenMP has no static library to link: ggml's own thread pool serves instead.
cmake -S "$src" -B "$b" -DCMAKE_BUILD_TYPE=Release "${cross[@]}" \
  -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=OFF -DGGML_BACKEND_DL=OFF -DGGML_OPENMP=OFF \
  -DLLAMA_CURL=OFF -DLLAMA_OPENSSL=OFF -DLLAMA_BUILD_HTML=OFF \
  -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF \
  -DCMAKE_EXE_LINKER_FLAGS=-static >/dev/null
# Each compiler takes about a gigabyte: no more of them than the builder holds.
jobs=$(nproc); [ "$jobs" -le 8 ] || jobs=8
cmake --build "$b" --target llama-server -j "$jobs" >/dev/null
install -D -m 0755 "$b/bin/llama-server" "$bin"
echo "llama-server $LLAMA_CPP_VERSION: $bin"
