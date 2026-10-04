#!/usr/bin/env bash
# Extracts the admin kubeconfig a development node printed on its console.
# Usage: hack/kubeconfig.sh <console log> > kubeconfig
set -euo pipefail
sed -n '/-----BEGIN KUBEROOT KUBECONFIG-----/,/-----END KUBEROOT KUBECONFIG-----/p' "${1:?console log required}" \
  | sed '1d;$d' | tr -d '\r' | base64 -d
