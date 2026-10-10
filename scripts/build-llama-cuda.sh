#!/usr/bin/env bash
# Builds llama-server for NVIDIA GPUs: CUDA's runtime and cuBLAS linked in,
# so the binary needs only the driver's libcuda and glibc on the node. CUDA
# comes from NVIDIA's redistributable archives, the few parts a build needs,
# unpacked under the builder's work directory. Runs in the builder.
# Usage: scripts/build-llama-cuda.sh <arch: amd64>
set -euo pipefail

ARCH=${1:?arch required}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=${WORK:-/work}
OUT=${KUBEROOT_OUT:-$ROOT/out}/$ARCH
. "$ROOT/build/components.env"
case "$ARCH" in
  amd64) platform=linux-x86_64; libdir=/lib/x86_64-linux-gnu ;;
  *) echo "llama-server for CUDA: built for amd64 only, not $ARCH"; exit 0 ;;
esac
bin="$OUT/llama-server-cuda-$LLAMA_CPP_VERSION"

# What it links against beyond the glibc runtime goes in the driver's tree,
# which build-nvidia.sh made.
deps() {
  [ -d "$OUT/nvidia$libdir" ] || { echo "no driver tree: run build-nvidia.sh first" >&2; exit 1; }
  for need in $(objdump -p "$bin" | awk '/NEEDED/{print $2}'); do
    case "$need" in libc.so.6|libresolv.so.2|libpthread.so.0|ld-linux*|libcuda*) continue ;; esac
    [ -e "$OUT/nvidia$libdir/$need" ] || cp -L "$libdir/$need" "$OUT/nvidia$libdir/"
  done
}
if [ -f "$bin" ]; then
  deps
  echo "llama-server-cuda $LLAMA_CPP_VERSION: $bin (built before)"
  exit 0
fi

cuda="$WORK/cache/cuda-$CUDA_REDIST_VERSION"
if [ ! -x "$cuda/bin/nvcc" ]; then
  base=https://developer.download.nvidia.com/compute/cuda/redist
  manifest=$(curl -fsSL "$base/redistrib_$CUDA_REDIST_VERSION.json")
  mkdir -p "$cuda.part"
  for c in cuda_nvcc cuda_cudart cuda_cccl libcublas cuda_nvrtc; do
    read -r path sha < <(printf '%s' "$manifest" | python3 -c "import json,sys; e=json.load(sys.stdin)['$c']['$platform']; print(e['relative_path'], e['sha256'])")
    archive="$WORK/cache/$(basename "$path")"
    # Large archives: resumed when the connection drops, used only whole;
    # one that is whole and still wrong is fetched anew.
    for try in 1 2 3 4 5 6; do
      echo "$sha  $archive" | sha256sum -c --quiet 2>/dev/null && break
      if curl -fsSL --retry 3 -C - -o "$archive" "$base/$path"; then
        echo "$sha  $archive" | sha256sum -c --quiet 2>/dev/null || : > "$archive"
      fi
    done
    echo "$sha  $archive" | sha256sum -c --quiet
    tar -xJf "$archive" -C "$cuda.part" --strip-components=1
  done
  rm -rf "$cuda"
  mv "$cuda.part" "$cuda"
fi
command -v cmake >/dev/null || { apt-get -qq update && apt-get -qq install -y cmake >/dev/null; }

src="$WORK/src/llama.cpp-$LLAMA_CPP_VERSION"
[ -d "$src" ] || git clone -q --depth 1 --branch "$LLAMA_CPP_VERSION" https://github.com/ggml-org/llama.cpp "$src"
b="$WORK/build-llama-cuda-$ARCH"
jobs=$(nproc); [ "$jobs" -le 8 ] || jobs=8
# Turing (T4), Ampere (A100, A10, 30xx), Ada (L4, 40xx), Hopper (H100).
PATH="$cuda/bin:$PATH" cmake -S "$src" -B "$b" -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_CUDA_COMPILER="$cuda/bin/nvcc" -DCUDAToolkit_ROOT="$cuda" \
  -DGGML_CUDA=ON -DGGML_STATIC=ON -DCMAKE_CUDA_ARCHITECTURES="75;80;86;89;90" \
  -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=OFF -DGGML_BACKEND_DL=OFF -DGGML_OPENMP=OFF \
  -DLLAMA_CURL=OFF -DLLAMA_OPENSSL=OFF -DLLAMA_BUILD_HTML=OFF -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF \
  -DCMAKE_EXE_LINKER_FLAGS="-static-libstdc++ -static-libgcc" >/dev/null
PATH="$cuda/bin:$PATH" cmake --build "$b" --target llama-server -j "$jobs" >/dev/null
install -D -m 0755 "$b/bin/llama-server" "$bin"
deps
echo "llama-server-cuda $LLAMA_CPP_VERSION: $bin, links $(objdump -p "$bin" | awk '/NEEDED/{printf "%s ", $2}')"
