#!/usr/bin/env bash
# 30-second reviewer demo: golden SFC translation + simulator contract (no Docker/envtest).
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH=".tools/go/bin:${PATH}"

echo "=== demo: golden SFC + simulator contract ==="
go test -count=1 -v -run 'TestTranslateSFC_MatchesGolden|TestSimulatorContract_MatchesGoldenYAML' ./...
echo ""
echo "DEMO PASSED — full lanes: ./scripts/verify-all.sh"
