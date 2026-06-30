# P5-B 框架健壮性三连 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修三处互不耦合的框架健壮性问题——B4 NatsRPC handler nil-guard、B5 集群 DieChan 死线桥接、B6 TCPAcceptor listen 失败优雅化——合一个 code PR。

**Architecture:** 三处局部/加性改动，各落一个文件 + 一个测试文件。B6 把 panic 换成对齐 WSAcceptor 的优雅返回；B4 在三个 handler 解引用点加 nil 守卫；B5 在 `Application.Run()` 增一条 select 分支接通集群 die 信号（框架级能力断言，零 `main.go` 改动）。全程 TDD，纯沙箱单测可验，无 proto / config / Docker 依赖。

**Tech Stack:** Go，标准库 `net`/`testing`，`project/src/common/logger`（nil-safe nopLogger 默认，测试无需 SetGlobal），`project/src/framework/cluster`（NodeID / NoopCluster）。

**设计依据：** `docs/plans/2026-06-06-P5-B-framework-robustness.md`（设计 Spec）。

**执行须知（沿 P5-A/P5-① 经验）：**
- 逐 Task TDD：先写失败测试 → 跑确认 fail → 最小实现 → 跑确认 pass → commit。
- line 号会漂——按下方代码文本匹配定位，勿信行号。
- gofmt 用 `$(go env GOROOT)/bin/gofmt`；只清本切片改动新增的 dirt，main 既有 dirt 不动。
- 禁直接 push main；本计划只在 feature 分支提交，PR 由控制器在用户授权后开。

---

## 文件结构

| 文件 | 责任 | 动作 |
|---|---|---|
| `src/framework/network/acceptor/tcp_acceptor.go` | TCP 监听器：listen 失败优雅返回（去 panic） | Modify |
| `src/framework/network/acceptor/tcp_acceptor_test.go` | B6 测试（包内首个测试文件） | Create |
| `src/framework/cluster/transport/nats_rpc.go` | 集群 RPC：三处 handler nil-guard | Modify |
| `src/framework/cluster/transport/nats_rpc_test.go` | B4 测试（追加到既有文件） | Modify |
| `src/framework/application/application.go` | 应用框架：接通集群 die 信号到停机 | Modify |
| `src/framework/application/application_test.go` | B5 测试（包内新建） | Create |

---

## Task 1: B6 — TCPAcceptor listen 失败优雅化

**Files:**
- Modify: `src/framework/network/acceptor/tcp_acceptor.go`（`ListenAndServe` 开头的 listen 错误分支 + import 块）
- Test: `src/framework/network/acceptor/tcp_acceptor_test.go`（新建）

- [ ] **Step 1: 写失败测试**

新建 `src/framework/network/acceptor/tcp_acceptor_test.go`：

```go
package acceptor

import (
	"net"
	"testing"
	"time"
)

// TestTCPAcceptor_ListenFailureGraceful 验证 listen 失败时不 panic，
// 而是关闭 connChan 并返回（对齐 WSAcceptor 行为）。
// 修复前：net.Listen 失败 → panic（在独立 goroutine 中无法 recover，崩整进程）。
func TestTCPAcceptor_ListenFailureGraceful(t *testing.T) {
	// 先占住一个端口，令同 addr 的 acceptor listen 必然失败
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to occupy port: %v", err)
	}
	defer busy.Close()

	a := NewTCPAcceptor(busy.Addr().String())

	// ListenAndServe 在 listen 失败时应立即返回（非阻塞）且不 panic。
	// recover 把修复前的 panic 转成干净的测试失败（而非崩溃测试二进制）。
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if rec := recover(); rec != nil {
				t.Errorf("ListenAndServe panicked on listen failure: %v", rec)
			}
		}()
		a.ListenAndServe()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndServe did not return on listen failure")
	}

	if a.IsRunning() {
		t.Fatal("acceptor should not be running after listen failure")
	}

	// connChan 应已被关闭（非阻塞校验，避免修复前未关闭导致测试 hang）
	select {
	case _, ok := <-a.ConnChan():
		if ok {
			t.Fatal("connChan should be closed, but received a connection")
		}
	case <-time.After(time.Second):
		t.Fatal("connChan was not closed after listen failure")
	}
}
```

- [ ] **Step 2: 跑测试确认 fail**

Run: `go test ./src/framework/network/acceptor/ -run TestTCPAcceptor_ListenFailureGraceful -v`
Expected: FAIL — `ListenAndServe panicked on listen failure: TCPAcceptor listen failed: ...`，且/或 `connChan was not closed`（修复前 panic 在 close 之前）。

- [ ] **Step 3: 最小实现**

修改 `src/framework/network/acceptor/tcp_acceptor.go`。先把 import 块加上 logger（当前为 `"net"` / `"sync/atomic"` / `"time"`）：

```go
import (
	"net"
	"sync/atomic"
	"time"

	"project/src/common/logger"
)
```

再把 `ListenAndServe` 开头的 listen 错误分支从 panic 改为优雅返回。原代码：

```go
func (a *TCPAcceptor) ListenAndServe() {
	ln, err := net.Listen("tcp", a.addr)
	if err != nil {
		panic("TCPAcceptor listen failed: " + err.Error())
	}
	a.listener.Store(&ln)
	a.running.Store(true)
```

改为：

```go
func (a *TCPAcceptor) ListenAndServe() {
	ln, err := net.Listen("tcp", a.addr)
	if err != nil {
		// 与 WSAcceptor 一致：listen 失败不 panic（panic 发生在 application.Start() 启动的
		// 独立 goroutine 中无法 recover，会崩整进程），改为记录错误 + 关闭 connChan
		// （通知消费方退出）后返回。失败时 running 尚未置 true，无需复位。
		logger.Error("tcp listen failed", logger.String("addr", a.addr), logger.Err(err))
		close(a.connChan)
		return
	}
	a.listener.Store(&ln)
	a.running.Store(true)
```

（其余函数体不变。）

- [ ] **Step 4: 跑测试确认 pass**

Run: `go test ./src/framework/network/acceptor/ -run TestTCPAcceptor_ListenFailureGraceful -v`
Expected: PASS

- [ ] **Step 5: 跑包内全量 + gofmt**

Run: `go test ./src/framework/network/acceptor/ -race` 然后 `$(go env GOROOT)/bin/gofmt -l src/framework/network/acceptor/tcp_acceptor.go src/framework/network/acceptor/tcp_acceptor_test.go`
Expected: 测试 PASS；gofmt 无输出（两文件已格式化）。

- [ ] **Step 6: Commit**

```bash
git add src/framework/network/acceptor/tcp_acceptor.go src/framework/network/acceptor/tcp_acceptor_test.go
git commit -m "$(cat <<'EOF'
fix(framework): TCPAcceptor listen 失败优雅返回，不再 panic（B6）

panic 发生在 application.Start() 启动的独立 goroutine 中无法 recover，会崩整进程；
改为对齐 WSAcceptor：logger.Error + close(connChan) + return。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: B4 — NatsRPC handler nil-guard

**Files:**
- Modify: `src/framework/cluster/transport/nats_rpc.go`（新增 `errHandlerNotSet` + `Call` / `CallAsync` / `handleMessage` 三处守卫）
- Test: `src/framework/cluster/transport/nats_rpc_test.go`（追加用例 + import）

- [ ] **Step 1: 写失败测试**

在 `src/framework/cluster/transport/nats_rpc_test.go` 末尾追加。先把 import 块补全（当前为 `"errors"` / `"testing"` / `proto` / `pb`），改为：

```go
import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"project/src/framework/cluster"
	"project/src/framework/cluster/pb"
)
```

追加三个测试：

```go
func TestCall_NilHandlerReturnsError(t *testing.T) {
	target := cluster.MakeNodeID(1, 1, 1)
	r := &NatsRPC{subject: target.Subject()} // 本地短路目标；handler 默认 nil
	_, err := r.Call(context.Background(), target, &pb.ClusterMessage{Route: "r", Data: []byte("x")})
	if !errors.Is(err, errHandlerNotSet) {
		t.Fatalf("expected errHandlerNotSet, got %v", err)
	}
}

func TestCallAsync_NilHandlerReturnsError(t *testing.T) {
	target := cluster.MakeNodeID(1, 1, 1)
	r := &NatsRPC{subject: target.Subject()}
	errCh := make(chan error, 1)
	r.CallAsync(context.Background(), target, &pb.ClusterMessage{Route: "r"}, func(_ []byte, err error) {
		errCh <- err
	})
	select {
	case err := <-errCh:
		if !errors.Is(err, errHandlerNotSet) {
			t.Fatalf("expected errHandlerNotSet, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("done callback not invoked")
	}
}

func TestHandleMessage_NilHandlerNoPanic(t *testing.T) {
	r := &NatsRPC{subject: "1.1.1"} // handler 默认 nil，conn 默认 nil
	body, err := proto.Marshal(&pb.ClusterMessage{Route: "r", Data: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	// Reply 为空（oneway）→ publishReply 提前返回不触碰 nil conn；
	// 修复前：r.handler 为 nil → nil-deref panic；修复后：守卫提前返回，无 panic。
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("handleMessage panicked with nil handler: %v", rec)
		}
	}()
	r.handleMessage(&nats.Msg{Reply: "", Data: body})
}
```

- [ ] **Step 2: 跑测试确认 fail**

Run: `go test ./src/framework/cluster/transport/ -run 'TestCall_NilHandler|TestCallAsync_NilHandler|TestHandleMessage_NilHandler' -v`
Expected: FAIL — 编译错误 `undefined: errHandlerNotSet`（守卫与变量尚未存在）。

- [ ] **Step 3: 最小实现**

修改 `src/framework/cluster/transport/nats_rpc.go`。

(a) 在 `const defaultCallTimeout = 5 * time.Second` 之后新增变量：

```go
// errHandlerNotSet 在 handler 注入前被解引用时返回，把潜在 nil-deref panic
// （会在分离的 NATS 回调 goroutine 中崩整进程）降级为干净错误（防御性）。
var errHandlerNotSet = errors.New("cluster: handler not set")
```

(b) `Call` 本地短路分支加守卫。原代码：

```go
	// 本地短路
	if target.Subject() == r.subject {
		return r.handler(ctx, msg.Data, msg.Route)
	}
```

改为：

```go
	// 本地短路
	if target.Subject() == r.subject {
		if r.handler == nil {
			return nil, errHandlerNotSet
		}
		return r.handler(ctx, msg.Data, msg.Route)
	}
```

(c) `CallAsync` 本地短路分支加守卫。原代码：

```go
	// 本地短路
	if target.Subject() == r.subject {
		go func() { done(r.handler(ctx, msg.Data, msg.Route)) }()
		return
	}
```

改为：

```go
	// 本地短路
	if target.Subject() == r.subject {
		if r.handler == nil {
			done(nil, errHandlerNotSet)
			return
		}
		go func() { done(r.handler(ctx, msg.Data, msg.Route)) }()
		return
	}
```

(d) `handleMessage` 在调 handler 前加守卫。原代码：

```go
	ctx = cluster.WithReplier(ctx, &natsReplier{conn: r.conn, route: cm.Route, reply: natsMsg.Reply})

	data, err := r.handler(ctx, cm.Data, cm.Route)
```

改为：

```go
	ctx = cluster.WithReplier(ctx, &natsReplier{conn: r.conn, route: cm.Route, reply: natsMsg.Reply})

	if r.handler == nil {
		logger.Warn("cluster: message received before handler set", logger.String("route", cm.Route))
		publishReply(r.conn, cm.Route, natsMsg.Reply, nil, errHandlerNotSet)
		return
	}

	data, err := r.handler(ctx, cm.Data, cm.Route)
```

- [ ] **Step 4: 跑测试确认 pass**

Run: `go test ./src/framework/cluster/transport/ -run 'TestCall_NilHandler|TestCallAsync_NilHandler|TestHandleMessage_NilHandler' -v`
Expected: PASS（三个用例全过）。

- [ ] **Step 5: 跑包内全量 + gofmt**

Run: `go test ./src/framework/cluster/transport/ -race` 然后 `$(go env GOROOT)/bin/gofmt -l src/framework/cluster/transport/nats_rpc.go src/framework/cluster/transport/nats_rpc_test.go`
Expected: 测试 PASS（含既有 `TestPublishReply_*`）；gofmt 无输出。

- [ ] **Step 6: Commit**

```bash
git add src/framework/cluster/transport/nats_rpc.go src/framework/cluster/transport/nats_rpc_test.go
git commit -m "$(cat <<'EOF'
fix(framework): NatsRPC handler nil-guard，防注入前解引用 panic（B4）

Call/CallAsync 本地短路与 handleMessage 直接解引用 r.handler；标准生命周期下
SetHandler 严格先于订阅故 nil 不可达，加守卫把"注入顺序未来漂移→分离 goroutine
nil-deref 崩进程"降级为干净 errHandlerNotSet（防御性）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: B5 — 集群 DieChan 死线桥接（触及停机生命周期，控制器给一次聚焦 spec+质量复核）

**背景（务必理解再改）：** 各 `main.go` 建 `NewNatsCluster` 未设 `DieChan` → 集群自建 cap-1 缓冲 `dieCh` 交给 NatsRPC/Discovery；etcd/NATS 永久故障时它们 `select{case dieCh<-:default:}` 发信号到 `cls.DieChan()`，但**全仓无接收方**。`app.Run()` 只 select `sigChan` + `a.dieChan`（另一条仅 `Stop` 自己关的 channel）。净效果=infra 故障时自动停机永不触发。本 Task 接通这条线。

**Files:**
- Modify: `src/framework/application/application.go`（新增 `clusterDieChan` + `awaitDie`，改 `Run`）
- Test: `src/framework/application/application_test.go`（新建）

- [ ] **Step 1: 写失败测试**

新建 `src/framework/application/application_test.go`：

```go
package application

import (
	"testing"
	"time"

	"project/src/framework/cluster"
)

// fakeDieCluster 嵌入 noopCluster 满足 cluster.Cluster 其余方法，
// 额外暴露 DieChan() 能力，模拟 NatsCluster。
type fakeDieCluster struct {
	cluster.Cluster
	die chan struct{}
}

func (f *fakeDieCluster) DieChan() <-chan struct{} { return f.die }

func TestClusterDieChan_WithCapability(t *testing.T) {
	die := make(chan struct{}, 1)
	a := &Application{cls: &fakeDieCluster{Cluster: cluster.NewNoopCluster(), die: die}}
	if got := a.clusterDieChan(); got != (<-chan struct{})(die) {
		t.Fatal("clusterDieChan should return the cluster's die channel")
	}
}

func TestClusterDieChan_WithoutCapability(t *testing.T) {
	a := &Application{cls: cluster.NewNoopCluster()} // noop 不暴露 DieChan
	if a.clusterDieChan() != nil {
		t.Fatal("clusterDieChan should be nil for a cluster without DieChan capability")
	}
}

func TestAwaitDie_ReturnsOnClusterDie(t *testing.T) {
	die := make(chan struct{}, 1)
	a := &Application{
		cls:     &fakeDieCluster{Cluster: cluster.NewNoopCluster(), die: die},
		dieChan: make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		// sigChan=nil 永久阻塞信号路；dieChan 不关；仅靠集群 die 信号唤醒
		a.awaitDie(nil)
		close(done)
	}()
	die <- struct{}{}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("awaitDie did not return on cluster die signal")
	}
}
```

- [ ] **Step 2: 跑测试确认 fail**

Run: `go test ./src/framework/application/ -run 'TestClusterDieChan|TestAwaitDie' -v`
Expected: FAIL — 编译错误 `a.clusterDieChan undefined` / `a.awaitDie undefined`。

- [ ] **Step 3: 最小实现**

修改 `src/framework/application/application.go`。

(a) 在 `Run` 方法之前新增两个方法：

```go
// clusterDieChan 返回集群的 die 信号 channel（集群实现暴露该能力时），否则返回 nil。
// 采用与 Start() 中 a.cls.(cluster.HandlerSetter) 相同的「可选能力」断言风格——
// DieChan 不进 cluster.Cluster 接口，noopCluster / 测试 fake 无需实现。
// nil channel 在 select 中永久阻塞，对无该能力的实现安全。
func (a *Application) clusterDieChan() <-chan struct{} {
	if dc, ok := a.cls.(interface{ DieChan() <-chan struct{} }); ok {
		return dc.DieChan()
	}
	return nil
}

// awaitDie 阻塞直到收到停机触发：OS 信号 / 自身 dieChan / 集群 die 信号。
// 抽成独立方法便于单测（无需安装信号处理器）。
func (a *Application) awaitDie(sigChan <-chan os.Signal) {
	select {
	case sig := <-sigChan:
		logger.Info("received signal", logger.String("signal", sig.String()))
	case <-a.dieChan:
	case <-a.clusterDieChan():
		logger.Warn("cluster signaled die, shutting down")
	}
}
```

(b) 改 `Run`。原代码：

```go
// Run 阻塞等待 SIGINT/SIGTERM，然后执行优雅关闭
func (a *Application) Run() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigChan:
		logger.Info("received signal", logger.String("signal", sig.String()))
	case <-a.dieChan:
	}
	a.Stop()
}
```

改为：

```go
// Run 阻塞等待 SIGINT/SIGTERM 或集群 die 信号，然后执行优雅关闭
func (a *Application) Run() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	a.awaitDie(sigChan)
	a.Stop()
}
```

- [ ] **Step 4: 跑测试确认 pass**

Run: `go test ./src/framework/application/ -run 'TestClusterDieChan|TestAwaitDie' -v`
Expected: PASS（三个用例全过）。

- [ ] **Step 5: 跑包内全量 + gofmt**

Run: `go test ./src/framework/application/ -race -count=3` 然后 `$(go env GOROOT)/bin/gofmt -l src/framework/application/application.go src/framework/application/application_test.go`
Expected: 测试 PASS（含既有 `forward_session_test`）；`-count=3` 无 flake；gofmt 无输出。

- [ ] **Step 6: Commit**

```bash
git add src/framework/application/application.go src/framework/application/application_test.go
git commit -m "$(cat <<'EOF'
fix(framework): 接通集群 DieChan 到停机，修 infra 故障僵尸进程（B5）

cls.DieChan() 此前全仓无接收方：etcd/NATS 永久故障时集群发 die 信号无人消费，
进程沦为僵尸、自动停机永不触发。Run() 增 select 分支经 clusterDieChan() 能力断言
（镜像 HandlerSetter 惯例，零 main.go 改动）接通；停机等待抽成可测的 awaitDie。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: 全量回归 + gofmt 收口

**Files:** 无新改动（仅验证；若发现新增 gofmt dirt 在此修）。

- [ ] **Step 1: 全量构建**

Run: `go build ./...`
Expected: 无输出（成功）。

- [ ] **Step 2: vet（含集成 tag 编译验证）**

Run: `go vet ./...` 然后 `go vet -tags integration ./...`
Expected: 两条均无输出（集成测试 `//go:build integration` 仅编译验证，沙箱无 Docker）。

- [ ] **Step 3: 全量 race 测试**

Run: `go test ./... -race`
Expected: 全部 `ok` / `no test files`（21 包全绿）。

- [ ] **Step 4: gofmt 全仓校验（只确认本切片三文件 + 两新建测试文件干净）**

Run: `$(go env GOROOT)/bin/gofmt -l src/framework/network/acceptor/ src/framework/cluster/transport/ src/framework/application/`
Expected: 本切片碰过的 5 个文件均不出现在输出里。若 `routes.go` / `session.go` / `online_module.go` 等 main 既有 dirt 出现，**不动**（CLAUDE.md「既有不主动改」）；仅当本切片新增文件出现才 `gofmt -w` 修。

- [ ] **Step 5: 无新提交则跳过；若 Step 4 修了本切片文件则 amend 到对应 Task commit**

（正常情况下 Task 1-3 的 Step 5 已确保各文件 gofmt 干净，本步通常无操作。）

---

## 自检（writing-plans Self-Review 结果）

**Spec 覆盖：** B4→Task 2；B5→Task 3；B6→Task 1；全量验证（§4）→Task 4；非目标（B1/B7/B2/B3/B6-failfast/§C docs）按 Spec §2/§7 明确排除，无遗漏任务。

**Placeholder：** 无 TBD/TODO；每个改动步骤含完整 before/after 代码与可执行命令 + 预期输出。

**类型一致性：** `errHandlerNotSet`（Task 2 定义并在三处引用）、`clusterDieChan()` / `awaitDie(sigChan <-chan os.Signal)`（Task 3 定义并在测试与 Run 中一致引用）、`fakeDieCluster.DieChan() <-chan struct{}`（与能力断言 `interface{ DieChan() <-chan struct{} }` 签名一致）、`NewTCPAcceptor(addr string)` / `ConnChan()` / `IsRunning()`（与既有签名一致）。测试中信道比较用显式转换 `(<-chan struct{})(die)` 规避可比较性歧义。
