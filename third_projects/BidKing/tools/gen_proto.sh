#!/bin/bash
set -euo pipefail
PROTO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROTO_DIR"

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
    protocol/handler/room_handler.proto \
    protocol/handler/match_handler.proto \
    protocol/handler/online_handler.proto \
    protocol/service/lobby_service.proto \
    protocol/service/room_service.proto \
    protocol/service/match_service.proto \
    protocol/service/online_service.proto \
    protocol/service/gate_service.proto

echo "=== 2. generating service stubs ==="
protoc --proto_path=. --proto_path=/usr/local/include \
    --svcstub_out=protocol/gen --svcstub_opt=paths=source_relative \
    protocol/handler/lobby_handler.proto \
    protocol/handler/room_handler.proto \
    protocol/handler/match_handler.proto \
    protocol/handler/online_handler.proto \
    protocol/service/lobby_service.proto \
    protocol/service/room_service.proto \
    protocol/service/match_service.proto \
    protocol/service/online_service.proto \
    protocol/service/gate_service.proto

echo "=== 3. generating route table ==="
go run tools/gen_routes/main.go --proto protocol/handler --out protocol/gen/routes.go

echo "=== 4. generating config schema descriptor ==="
protoc \
  --proto_path=. \
  --descriptor_set_out=conf/schema/gen/config.pb.descriptor \
  --include_imports \
  conf/schema/*.proto

echo "=== 5. generating config Go code ==="
go run tools/gen_config/main.go

echo "=== 6. formatting generated Go files ==="
gofmt -w protocol/gen/ conf/schema/gen/ 2>/dev/null || true

echo "=== all done ==="
