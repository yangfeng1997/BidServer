# P5-A §9.3 死代码/占位清理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 删除被新架构淘汰的占位目录、示例协议与孤儿生成物，重生成路由表，并把残留的 world/scene/login 源注释改到目标拓扑——使代码库与 gateway/lobby/router/online/match/room 拓扑一致。

**Architecture:** 纯机械清理切片。无新行为、无新单元测试可 TDD——**验证 = `go build/vet/test ./... -race` 全绿 + grep 断言 + `gen_routes` 幂等 + 「生成 `.pb.go` 零 churn」**（每个删除/编辑步骤后立即跑命令断言期望输出，命令即测试）。`tools/gen_routes` 是纯文本解析器（**不需要 protoc**）；本切片**不调用 protoc**、**不触碰任何生成 `.pb.go` 内容**（除 `routes.go` 文本重生与删除孤儿/world 生成物）。

**Tech Stack:** Go 1.25；`go run ./tools/gen_routes`（文本重生路由表）；`git rm`；`$(go env GOROOT)/bin/gofmt`（gofmt 不在 PATH）。

**设计依据：** [`2026-06-05-P5-A-cleanup.md`](2026-06-05-P5-A-cleanup.md)（spec，含 §2 现状核实、§5 决策 D-A1 修订/D-A2/D-A3/D-A4）。

**serverTypeID 事实（conf/values/dev.yaml，供注释示例用）：** gate=1、lobby=2、online=5、router=6、**room=7（nodeID `1.7.1`）**、match=8；类型 3/4 已释放（原 world/scene）。

---

## Task 1: 删占位/示例/孤儿生成物 + 重生 routes.go

**Files:**
- Delete: `src/servers/dbsvr/`（仅 `.gitkeep`）、`src/servers/loginsvr/`（仅 `.gitkeep`）
- Delete: `protocal/world.proto`、`protocal/gen/world/`（整目录，仅 `world.pb.go`）
- Delete: `protocal/gen/protocal/cluster.pb.go`（孤儿重复生成物，零 importer；连带空目录）
- Regenerate: `protocal/gen/routes/routes.go`（`go run ./tools/gen_routes`）

- [ ] **Step 1: 基线断言（确认清理目标确实存在 = "red"）**

Run:
```bash
cd /game/GameServer
grep -nE "3001|3003|3101|WorldHandler|SceneHandler" protocal/gen/routes/routes.go
ls protocal/gen/protocal/cluster.pb.go protocal/world.proto src/servers/dbsvr/.gitkeep src/servers/loginsvr/.gitkeep
go build ./...
```
Expected: grep 命中 5 条 world/scene 路由（3001/3003/3101 + WorldHandler + SceneHandler）；`ls` 全部存在；`go build ./...` 当前**绿**（routes.go 的 world 条目是字符串值，删 world.proto 前 build 不受影响）。

- [ ] **Step 2: 删除占位目录、示例协议、孤儿生成物**

Run:
```bash
cd /game/GameServer
git rm -r src/servers/dbsvr src/servers/loginsvr protocal/gen/world
git rm protocal/world.proto protocal/gen/protocal/cluster.pb.go
```
Expected: `git rm` 成功列出删除的文件（dbsvr/.gitkeep、loginsvr/.gitkeep、gen/world/world.pb.go、world.proto、gen/protocal/cluster.pb.go）。

- [ ] **Step 3: 重生成 routes.go（文本解析，无需 protoc）**

Run:
```bash
cd /game/GameServer
go run ./tools/gen_routes
```
Expected: 无报错；`protocal/gen/routes/routes.go` 被重写。

- [ ] **Step 4: 验证 routes.go 已无 world/scene + build 绿 + 幂等**

Run:
```bash
cd /game/GameServer
grep -nE "3001|3003|3101|WorldHandler|SceneHandler|worldsvr|scenesvr" protocal/gen/routes/routes.go || echo "ROUTES_CLEAN"
go build ./...
go run ./tools/gen_routes && git diff --quiet protocal/gen/routes/routes.go && echo "GEN_ROUTES_IDEMPOTENT"
grep -rnE "gen/world|gen/protocal|world\.proto|src/servers/dbsvr|src/servers/loginsvr" --include=*.go . | grep -v "_test" || echo "NO_DANGLING_REFS"
```
Expected: 第一条打印 `ROUTES_CLEAN`（无任何 world/scene 残留）；`go build ./...` **绿**；打印 `GEN_ROUTES_IDEMPOTENT`（再跑一次无 diff）；打印 `NO_DANGLING_REFS`。

- [ ] **Step 5: Commit**

```bash
cd /game/GameServer
git add -A
git commit -m "$(cat <<'EOF'
chore(cleanup): 删 dbsvr/loginsvr 占位 + world 示例协议 + 孤儿 gen/protocal，重生 routes（P5-A §9.3）

- 删 src/servers/dbsvr、src/servers/loginsvr（仅 .gitkeep 空占位，零引用）
- 删 protocal/world.proto + protocal/gen/world（worldsvr/scenesvr 示例，零 importer 死代码）
- 删 protocal/gen/protocal/cluster.pb.go（孤儿重复生成物，零 importer；规范在 src/framework/cluster/pb）
- go run ./tools/gen_routes 重生 routes.go：移除 3001/3003/3101 World/Scene 条目

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: 纠正 options.proto / cluster.proto 源注释

**Files:**
- Modify: `protocal/options.proto:14-16`
- Modify: `protocal/cluster.proto:10`

> **注意：仅改 `.proto` 源。** 不重生成 `options.pb.go` / `cluster.pb.go`（D-A1 修订：沙箱缺 v1.36.11 toolchain，清洁重生不可达；生成镜像延 §C 自愈）。`gen_routes` 跳过 options.proto/cluster.proto，故本任务不影响 routes.go。

- [ ] **Step 1: 改 options.proto msg_id 区段分配注释**

`protocal/options.proto` 当前（14-16 行）：
```
  //   2000 - 2999 : loginsvr
  //   3000 - 3999 : worldsvr
  //   4000 - 4999 : scenesvr
```
改为：
```
  //   2000 - 2999 : lobbysvr
  //   3000 - 3999 : （已释放，预留扩展）
  //   4000 - 4999 : （已释放，预留扩展）
```
（`2000-2999: loginsvr` 是陈旧 bug——lobbysvr 实占 2001-2042；3xxx/4xxx 随 world/scene 淘汰而释放。1000-1999:gatesvr、5000+:预留 两行不动。）

- [ ] **Step 2: 改 cluster.proto 字段示例注释**

`protocal/cluster.proto:10` 当前：
```
  string server_type_name = 2; // 服务类型名称，如 "worldsvr"
```
改为：
```
  string server_type_name = 2; // 服务类型名称，如 "lobbysvr"
```

- [ ] **Step 3: 验证仅源改、生成物零 churn、build 绿**

Run:
```bash
cd /game/GameServer
grep -nE "loginsvr|worldsvr|scenesvr" protocal/options.proto protocal/cluster.proto || echo "PROTO_SRC_CLEAN"
git diff --stat -- '*.pb.go' | grep -vE "^$" || echo "NO_PBGO_CHURN"
go build ./...
```
Expected: 打印 `PROTO_SRC_CLEAN`（源注释已无 loginsvr/worldsvr/scenesvr）；打印 `NO_PBGO_CHURN`（工作树无任何 `.pb.go` 改动）；`go build ./...` **绿**。

- [ ] **Step 4: Commit**

```bash
cd /game/GameServer
git add protocal/options.proto protocal/cluster.proto
git commit -m "$(cat <<'EOF'
docs(proto): 纠 options/cluster 源注释到目标拓扑（loginsvr→lobbysvr、释放 3xxx/4xxx；P5-A §9.3）

仅改 .proto 源注释（inert doc comment）；生成 .pb.go 镜像延 §C（沙箱缺 v1.36.11 toolchain，清洁重生不可达，下次正规重生自愈）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: 框架手写源示例注释改目标拓扑

**Files:**
- Modify: `src/framework/application/options.go:13`
- Modify: `src/framework/application/builder.go:43`
- Modify: `src/framework/application/application.go:212-213`
- Modify: `src/framework/session/session.go:15,47`

> 全部为手写源文件的 inert 注释（无生成镜像）。排除 `logger`/`timewheel` 的无关 `scenesvr` 示例字符串、`discovery.go` 的 `world-{worldID}` 命名空间（spec §4）。

- [ ] **Step 1: 改 options.go:13**

当前：`// 后端节点（worldsvr、scenesvr 等）不应调用此选项。`
改为：`// 后端节点（lobbysvr、roomsvr 等）不应调用此选项。`

- [ ] **Step 2: 改 builder.go:43**

当前：`// NodeType 设置节点类型，如 "gatesvr"、"worldsvr"`
改为：`// NodeType 设置节点类型，如 "gatesvr"、"lobbysvr"`

- [ ] **Step 3: 改 application.go:212-213**

当前：
```
// - 优先查 session 中该 serverType 的节点绑定（有状态服务，如 scenesvr）
// - 无绑定则随机选节点（无状态服务，如 loginsvr）
```
改为：
```
// - 优先查 session 中该 serverType 的节点绑定（有状态服务，如 roomsvr）
// - 无绑定则随机选节点（无状态服务，如 routersvr）
```

- [ ] **Step 4: 改 session.go:15 与 47**

第 15 行当前：
```
	boundNodes  map[string]string // serverTypeName → nodeID 点分格式，如 "scenesvr" → "1.4.2"
```
改为：
```
	boundNodes  map[string]string // serverTypeName → nodeID 点分格式，如 "roomsvr" → "1.7.1"
```
第 47 行当前：
```
// 典型用法：玩家进入场景后，gate 记录 "scenesvr" → "1.4.2"
```
改为：
```
// 典型用法：玩家进入对局后，gate 记录 "roomsvr" → "1.7.1"
```
（room serverTypeID=7 → nodeID `1.7.1`，与 conf/values/dev.yaml 一致。）

- [ ] **Step 5: 验证 build/vet 绿、目标文件无旧示例、生成物零 churn**

Run:
```bash
cd /game/GameServer
grep -rnE "worldsvr|scenesvr|loginsvr" src/framework/application/options.go src/framework/application/builder.go src/framework/application/application.go src/framework/session/session.go || echo "FRAMEWORK_SRC_CLEAN"
git diff --stat -- '*.pb.go' | grep -vE "^$" || echo "NO_PBGO_CHURN"
go build ./... && go vet ./...
"$(go env GOROOT)/bin/gofmt" -l src/framework/application/options.go src/framework/application/builder.go src/framework/application/application.go src/framework/session/session.go || true
```
Expected: 打印 `FRAMEWORK_SRC_CLEAN`；打印 `NO_PBGO_CHURN`；`go build`+`go vet` **绿**；`gofmt -l` **无输出**（4 个文件均 gofmt-clean）。

- [ ] **Step 6: Commit**

```bash
cd /game/GameServer
git add src/framework/application/options.go src/framework/application/builder.go src/framework/application/application.go src/framework/session/session.go
git commit -m "$(cat <<'EOF'
docs(framework): 源注释 world/scene/login 示例改到目标拓扑（P5-A §9.3）

options.go/builder.go/application.go/session.go 的 worldsvr/scenesvr/loginsvr 示例改为 lobbysvr/roomsvr/routersvr（nodeID 与 conf 一致）。排除 logger/timewheel 无关示例、discovery world-{worldID} 命名空间。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: 全量回归 + 验收门

**Files:** 无改动（纯验证门；仅当发现 gofmt 脏才补一次 style commit）。

- [ ] **Step 1: 全量构建/检查/测试（含 -race 与 integration tag 编译）**

Run:
```bash
cd /game/GameServer
go build ./...
go vet ./...
go vet -tags integration ./...
go test ./... -race
```
Expected: 四条全部 **绿**，无 FAIL、无 build 失败、无 data race。

- [ ] **Step 2: §6 验收 grep（清理彻底性 + 生成物零 churn）**

Run:
```bash
cd /game/GameServer
# 2a routes.go 无 world/scene
grep -nE "3001|3003|3101|WorldHandler|SceneHandler|worldsvr|scenesvr" protocal/gen/routes/routes.go || echo "OK_ROUTES_CLEAN"
# 2b 全仓无淘汰物引用（排除本计划/spec 文档自身）
grep -rnE "gen/world|gen/protocal|world\.proto|servers/dbsvr|servers/loginsvr" --include=*.go . || echo "OK_NO_REFS"
# 2c gen_routes 幂等
go run ./tools/gen_routes && git diff --quiet && echo "OK_IDEMPOTENT"
# 2d 生成 .pb.go：相对 main 仅删除（world.pb.go、gen/protocal/cluster.pb.go），无任何 .pb.go 被「修改」
git diff --stat main...HEAD -- '*.pb.go'
```
Expected: `OK_ROUTES_CLEAN`、`OK_NO_REFS`、`OK_IDEMPOTENT`；2d 的 `git diff --stat` **只列删除行**（`protocal/gen/world/world.pb.go` 与 `protocal/gen/protocal/cluster.pb.go`），**不含任何 `.pb.go` 的修改**（options.pb.go / src/framework/cluster/pb/cluster.pb.go 等不出现为 modify）。

- [ ] **Step 3: gofmt 全量收口（若脏）**

Run:
```bash
cd /game/GameServer
"$(go env GOROOT)/bin/gofmt" -l $(git diff --name-only main...HEAD -- '*.go')
```
Expected: **无输出**。若有输出（仅限本切片新改文件，不含 origin/main 既有 pre-existing dirt），`gofmt -w` 之并补一次 `style:` commit；既有 dirt（如 `gen_routes` 输出、main 上 directory.go 等）不动。

- [ ] **Step 4: 终态确认**

Run:
```bash
cd /game/GameServer
git log --oneline main..HEAD
git status --short
```
Expected: 3 个功能 commit（Task 1/2/3）+ 至多 1 个 style commit；`git status` 干净（无未提交改动）。

---

## Self-Review（计划 vs spec）

- **§3.1 删占位** → Task 1 Step 2 ✅
- **§3.2 删 world.proto+gen/world** → Task 1 Step 2 ✅
- **§3.3 删孤儿 gen/protocal/cluster.pb.go** → Task 1 Step 2 ✅
- **§3.4 重生 routes.go** → Task 1 Step 3-4 ✅
- **§3.5 纠 options.proto 区段注释（仅源）** → Task 2 Step 1 ✅
- **§3.6 框架示例注释（options/builder/application/session 手写源 + cluster.proto 源）** → Task 2 Step 2（cluster.proto）+ Task 3（4 手写源）✅
- **§3.7 清孤儿 import/常量** → 删除/注释无新增 import；Task 1/3 build+vet 验证无悬挂 ✅
- **§4 排除项**（生成 .pb.go 镜像、discovery world-{worldID}、logger/timewheel scenesvr）→ 计划中无任何步骤触碰，Task 4 Step 2d 主动断言 .pb.go 零修改 ✅
- **§6 验收 1-6** → Task 4 Step 1（1）/ Step 2a（2）/ Step 2c（3）/ Step 2b（4）/ Task 2 Step 3 + Task 3 Step 5（5）/ Task 4 Step 2d（6）✅
- **占位/类型一致性**：无新函数/类型引入（纯删除 + 注释）；所有命令与文件路径均为绝对/仓库相对实路径。
- **轻量化（D-A3）**：4 Task、删除/注释为主、命令即测试、无双评审强制——subagent 执行各 Task 后轻评审 + Task 4 全量绿即可。
