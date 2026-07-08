#!/usr/bin/env bash
# BF-3 hardware e2e lane (§13.1 bf3-hardware-v1).
# Contract gate always runs via TestBF3LaneSpec_Complete in verify.sh.
# Full hardware run: export BF3_LAB=1 KUBECONFIG=<bf3-lab-kubeconfig>
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH=".tools/go/bin:${PATH}"

echo "=== BF-3 lane contract (always) ==="
CGO_ENABLED=0 go test -run TestBF3LaneSpec_Complete -count=1 -v .

if [ "${BF3_LAB:-0}" != "1" ]; then
  echo ""
  echo "BF-3 HARDWARE LANE: contract verified; full lab run skipped (set BF3_LAB=1 + KUBECONFIG)"
  echo "Lane spec: testdata/hardware/bf3-lane.yaml"
  echo "CI job: .github/workflows/verify.yml (integration + kind on Linux runners)"
  exit 0
fi

if [ -z "${KUBECONFIG:-}" ]; then
  echo "FAIL: BF3_LAB=1 requires KUBECONFIG pointing at BF-3 lab cluster" >&2
  exit 1
fi

echo "=== BF-3 lab: golden SFC apply on real apiserver ==="
CGO_ENABLED=0 go test -tags bf3 -count=1 -v ./...

echo ""
echo "BF-3 HARDWARE E2E PASSED (lab gate)"
