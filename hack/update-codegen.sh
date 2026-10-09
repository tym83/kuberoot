#!/usr/bin/env bash
# Regenerates deepcopy, conversion and OpenAPI code for the node.kuberoot.dev API.
set -euo pipefail
cd "$(dirname "$0")/.."

OPENAPI=k8s.io/kube-openapi/cmd/openapi-gen@v0.0.0-20261001230523-97fa35140926
HEADER=hack/boilerplate.go.txt

go run k8s.io/code-generator/cmd/deepcopy-gen@v0.37.1 --go-header-file $HEADER \
  --output-file zz_generated.deepcopy.go ./pkg/apis/node/...
go run k8s.io/code-generator/cmd/conversion-gen@v0.37.1 --go-header-file $HEADER \
  --output-file zz_generated.conversion.go ./pkg/apis/node/v1alpha1
go run $OPENAPI --go-header-file $HEADER \
  --output-dir pkg/generated/openapi --output-pkg github.com/tym83/kuberoot/pkg/generated/openapi \
  --output-file zz_generated.openapi.go \
  k8s.io/apimachinery/pkg/apis/meta/v1 k8s.io/apimachinery/pkg/runtime k8s.io/apimachinery/pkg/version \
  ./pkg/apis/node/v1alpha1
