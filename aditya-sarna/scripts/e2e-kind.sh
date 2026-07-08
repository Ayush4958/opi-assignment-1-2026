#!/usr/bin/env bash
# Kind e2e lane (§13.1 compatibility.yaml ci.e2eLane: kind-nvidia-v1alpha1).
# Creates a throwaway cluster, installs CRDs, runs e2e-tagged Go tests.
set -euo pipefail
cd "$(dirname "$0")/.."

CLUSTER="${E2E_KIND_CLUSTER:-opi-nvidia-e2e}"

skip_or_fallback() {
  local reason="$1"
  if [ "${E2E_REQUIRE_KIND:-0}" = "1" ]; then
    echo "FAIL Kind e2e (required in CI): $reason" >&2
    exit 1
  fi
  echo "SKIP Kind e2e: $reason"
  if [ "$reason" = "docker daemon not running" ]; then
    echo "=== envtest e2e fallback (same golden-object lane; CI runs real Kind on Linux) ==="
    ENVTEST_DIR="$(./scripts/fetch-envtest.sh)"
    export KUBEBUILDER_ASSETS="$ENVTEST_DIR"
    export USE_ENVTEST_E2E=1
    CGO_ENABLED=0 PATH=".tools/go/bin:${PATH}" go test -tags e2e -count=1 -v ./...
    echo ""
    echo "E2E LANE PASSED (envtest fallback; start Docker + re-run for full Kind cluster proof)"
  fi
  exit 0
}

if ! command -v kind &>/dev/null; then
  skip_or_fallback "kind not installed (https://kind.sigs.k8s.io/)"
fi
if ! command -v kubectl &>/dev/null; then
  skip_or_fallback "kubectl not installed"
fi
if ! command -v docker &>/dev/null; then
  skip_or_fallback "docker not available"
fi
if ! docker info &>/dev/null; then
  skip_or_fallback "docker daemon not running"
fi

cleanup() {
  kind delete cluster --name "$CLUSTER" 2>/dev/null || true
}
trap cleanup EXIT

echo "=== Kind e2e: creating cluster $CLUSTER ==="
kind create cluster --name "$CLUSTER" --wait 120s

echo "=== Installing DPF + OPI stand-in CRDs ==="
kubectl apply -f testdata/crds/

export KUBECONFIG
KUBECONFIG="$(mktemp)"
kind get kubeconfig --name "$CLUSTER" > "$KUBECONFIG"
export KUBECONFIG

echo "=== go test -tags e2e ==="
PATH=".tools/go/bin:${PATH}" go test -tags e2e -count=1 -v ./...

echo ""
echo "KIND E2E PASSED"
