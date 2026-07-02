#!/bin/bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"
export PATH="$ROOT_DIR/bin:$PATH"

echo "=== 1. generating .pb.go files ==="
protoc --proto_path=. --proto_path=/usr/local/include \
    --go_out=. --go_opt=paths=source_relative \
    protocol/common/node.proto \
    protocol/common/options.proto \
    protocol/common/errcode.proto \
    protocol/ra/ra.proto \
    protocol/cs/lobby_cs.proto \
    protocol/ss/lobby_ss.proto \
    protocol/handler/lobby_handler.proto \
    protocol/remote/lobby_remote.proto \
    protocol/remote/gate_remote.proto

echo "=== 2. generating handler and remote stubs ==="
protoc --proto_path=. --proto_path=/usr/local/include \
    --svcstub_out=protocol/gen --svcstub_opt=paths=source_relative \
    protocol/handler/lobby_handler.proto \
    protocol/remote/lobby_remote.proto \
    protocol/remote/gate_remote.proto

echo "=== 3. generating route table ==="
go run tools/gen_routes/main.go --proto protocol/handler --out protocol/gen/routes.go

echo "=== 4. formatting generated Go files ==="
gofmt -w protocol/gen/ 2>/dev/null || true

echo "=== all done ==="
