#!/bin/bash
# 生成 protobuf 代码
# 用法: ./tools/gen_proto.sh [proto文件] 或不传参数生成所有
# 例: ./tools/gen_proto.sh protocal/cluster.proto
#     ./tools/gen_proto.sh  (生成 protocal/ 下所有 .proto)

set -e

PROTO_DIR="protocal"
MODULE="project"

if [ "$#" -gt 0 ]; then
  # 指定文件
  for f in "$@"; do
    echo "generating $f ..."
    protoc --go_out=. --go_opt=module="$MODULE" "$f"
  done
else
  # 生成所有（跳过 options.proto 依赖顺序问题，先生成 options）
  echo "generating all protos in $PROTO_DIR/ ..."
  protoc --go_out=. --go_opt=module="$MODULE" "$PROTO_DIR/options.proto" 2>/dev/null || true
  for f in "$PROTO_DIR"/*.proto; do
    [ "$f" = "$PROTO_DIR/options.proto" ] && continue
    echo "  $f"
    protoc --go_out=. --go_opt=module="$MODULE" --proto_path=. "$f"
  done
fi

# 重新生成路由表
echo "regenerating route tables ..."
go run ./tools/gen_routes

echo "done."
