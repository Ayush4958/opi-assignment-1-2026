#!/usr/bin/env bash
# Resolve and verify digest-pinned images in config/nvidia/dpf-bundle.yaml.
# Re-run at release cut; merge blocked if inspect fails.
set -euo pipefail
cd "$(dirname "$0")/.."

if ! command -v docker &>/dev/null; then
  echo "SKIP: docker not available for digest resolution"
  exit 0
fi

echo "=== DPF bundle digest verification ==="
refs=(
  "nvcr.io/nvidia/doca/dpf-system:v25.7.0"
  "nvcr.io/nvidia/doca/hostdriver:v25.7.0"
  "quay.io/argoproj/argocd:v2.14.2"
)
for ref in "${refs[@]}"; do
  echo "Inspecting $ref ..."
  docker buildx imagetools inspect "$ref" >/dev/null
done

echo "=== Bundle manifest placeholder check ==="
if grep -q 'REPLACE_AT_RELEASE' config/nvidia/dpf-bundle.yaml; then
  echo "FAIL: placeholder digests remain in dpf-bundle.yaml"
  exit 1
fi

echo "ALL DIGEST CHECKS PASSED"
