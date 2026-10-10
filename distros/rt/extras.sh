# Sourced by build-rootfs.sh: cyclictest, which the node's latency tests
# run, as build-rt-tests.sh built it.
. "$ROOT/build/components.env"
install -m 0755 "$OUT/cyclictest-$RT_TESTS_VERSION" $r/usr/bin/cyclictest
