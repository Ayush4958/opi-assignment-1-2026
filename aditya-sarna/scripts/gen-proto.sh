#!/usr/bin/env bash
# Generate api/vsp/*.pb.go from vsp.proto (requires protoc + plugins on PATH).
set -euo pipefail
cd "$(dirname "$0")/.."

if ! command -v protoc &>/dev/null; then
  if [ -x ".tools/protoc/bin/protoc" ]; then
    export PATH="$(pwd)/.tools/protoc/bin:${PATH}"
  else
    echo "protoc not installed; using committed api/vsp/*.pb.go" >&2
    exit 0
  fi
fi

export PATH="$(go env GOPATH)/bin:${PATH}"
protoc \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  api/vsp/vsp.proto

echo "Generated api/vsp/vsp.pb.go and vsp_grpc.pb.go"
