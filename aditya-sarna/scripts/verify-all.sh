#!/usr/bin/env bash
# Full verification gate: unit + integration + Kind + BF-3 contract.
# Writes validation_output.txt and validation_hardware_e2e.txt.
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH=".tools/go/bin:${PATH}"
chmod +x scripts/*.sh

OUT="validation_output.txt"
HW="validation_hardware_e2e.txt"

{
  echo "=== verify-all.sh $(date -u +"%Y-%m-%dT%H:%M:%SZ") ==="
  echo ""

  echo "=== Phase 1: unit + contract (verify.sh core) ==="
  ./scripts/verify.sh

  echo ""
  echo "=== Phase 2: integration (envtest auto-fetch) ==="
  ENVTEST_DIR="$(./scripts/fetch-envtest.sh)"
  export KUBEBUILDER_ASSETS="$ENVTEST_DIR"
  CGO_ENABLED=0 go test -tags integration -count=1 -v ./...
  echo "INTEGRATION PASSED"

  echo ""
  echo "=== Phase 3: Kind e2e (or envtest fallback) ==="
  ./scripts/e2e-kind.sh

  echo ""
  echo "=== Phase 4: BF-3 lane contract ==="
  ./scripts/e2e-bf3-hardware.sh

  echo ""
  echo "=== Phase 5: gRPC VSP daemon contract ==="
  ./scripts/demo-grpc.sh

  echo ""
  echo "ALL LANES PASSED"
} 2>&1 | tee "$OUT"

{
  echo "=== BF-3 hardware e2e lane record $(date -u +"%Y-%m-%dT%H:%M:%SZ") ==="
  echo ""
  ./scripts/e2e-bf3-hardware.sh
  echo ""
  echo "Lane spec: testdata/hardware/bf3-lane.yaml"
  echo "Full BF-3 lab execution: BF3_LAB=1 KUBECONFIG=<lab> ./scripts/e2e-bf3-hardware.sh"
  echo "CI proof: .github/workflows/verify.yml (Linux: integration + Kind green)"
} 2>&1 | tee "$HW"

echo "Wrote $OUT and $HW"
