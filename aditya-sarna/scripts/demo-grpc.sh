#!/usr/bin/env bash
# gRPC VSP daemon demo: bufconn contract tests + live TCP smoke + optional two-process walkthrough.
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH=".tools/go/bin:${PATH}"

echo "=== gRPC VSP contract tests (bufconn) ==="
CGO_ENABLED=0 go test -count=1 -v ./vspgrpc/... -run 'TestVSPDaemon|TestGRPCDaemon_LivePing'

echo ""
echo "=== gRPC VSP live TCP smoke (in-test daemon on ephemeral port) ==="
CGO_ENABLED=0 go test -count=1 -v ./vspgrpc/... -run TestLiveSmoke_TCPRoundTrip

if [[ "${VSP_LIVE_DEMO:-}" == "1" ]]; then
  echo ""
  echo "=== Two-process live demo (vspdaemon + vspclient) ==="
  PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
  ADDR="127.0.0.1:${PORT}"
  CGO_ENABLED=0 go run ./cmd/vspdaemon -addr ":${PORT}" -seed-nf &
  PID=$!
  trap 'kill "$PID" 2>/dev/null || true' EXIT
  python3 -c 'import socket,sys,time
port=int(sys.argv[1])
for _ in range(50):
  s=socket.socket()
  try:
    s.settimeout(0.2)
    s.connect(("127.0.0.1", port))
    s.close()
    sys.exit(0)
  except OSError:
    time.sleep(0.1)
sys.exit(1)' "$PORT"
  CGO_ENABLED=0 go run ./cmd/vspclient -addr "$ADDR" -nf
  echo "Two-process demo OK on ${ADDR}"
fi

echo ""
echo "GRPC DEMO PASSED"
echo ""
echo "Manual live walkthrough:"
echo "  Terminal 1: go run ./cmd/vspdaemon -addr :50051 -seed-nf"
echo "  Terminal 2: go run ./cmd/vspclient -addr localhost:50051 -nf"
echo "  (or set VSP_LIVE_DEMO=1 ./scripts/demo-grpc.sh for automated two-process demo)"
