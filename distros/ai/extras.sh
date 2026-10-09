# Sourced by build-rootfs.sh: llama-server, the model server, which
# build-llama.sh built.
. "$ROOT/build/components.env"
install -m 0755 "$OUT/llama-server-$LLAMA_CPP_VERSION" $r/usr/bin/llama-server
