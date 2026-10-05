#!/usr/bin/env bash
# Runs the unit tests on Linux: much of kuberoot uses Linux-only system calls,
# so on macOS the tests run in a container.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ "$(uname -s)" = Linux ]; then
  exec go test ./...
fi
ARCH=$(go env GOARCH)
# Under the project, which colima shares with its VM; the system temp dir is not.
OUT=$PWD/.cache/tests
rm -rf "$OUT" && mkdir -p "$OUT"
for pkg in $(go list ./...); do
  name=$(go list -f '{{.Dir}}' "$pkg" | sed "s|^$PWD/||" | tr / _)
  # Only packages with tests produce a binary.
  GOOS=linux GOARCH=$ARCH go test -c -o "$OUT/$name.test" "$pkg" >/dev/null
done
status=0
for bin in "$OUT"/*.test; do
  [ -e "$bin" ] || continue
  # Run in the package directory, as go test does, so tests find their files.
  dir=$(basename "$bin" .test | tr _ /)
  if docker run --rm -v "$OUT:/t:ro" -v "$PWD:/src:ro" -w "/src/$dir" alpine:3.22 "/t/$(basename "$bin")" -test.v > "$bin.log" 2>&1; then
    echo "ok   $(basename "$bin" .test)"
  else
    echo "FAIL $(basename "$bin" .test)"; cat "$bin.log"; status=1
  fi
done
exit $status
