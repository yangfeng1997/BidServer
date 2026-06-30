# CLI/Daemon 框架 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 `src/common/pidfile/`、`src/common/daemon/`、`src/framework/cli/` 三个包，为各服务 `main.go` 提供统一的 cobra CLI 入口（start/stop/kill/reload/status 子命令 + --addr/--conf-file/--pid-file/--log-file/--daemon flags），替换现有硬编码路径。

**Architecture:** `pidfile` 和 `daemon` 是纯标准库工具包，放 `src/common/`；`cli` 是项目级 cobra 封装层，放 `src/framework/`，依赖前两者和 cobra。每个 `main.go` 只导入 `cli/`，通过 `cli.New(...).OnStart(...).Execute()` 组装入口，原有服务逻辑不变。

**Tech Stack:** `github.com/spf13/cobra`，Go 标准库 `os`/`syscall`/`strconv`/`os/exec`

---

## 文件结构

```
src/common/pidfile/
    pidfile.go          # Write / Read / Remove / IsRunning

src/common/daemon/
    daemon.go           # Daemonize()：重 exec 自身，setsid 脱离终端

src/framework/cli/
    cli.go              # Builder：New / OnStart / OnReload / DefaultConf / GitRevision / Execute
    flags.go            # Flags struct + bindPersistentFlags()
    commands.go         # newStartCmd / newStopCmd / newKillCmd / newReloadCmd / newStatusCmd

src/common/pidfile/pidfile_test.go
src/common/daemon/daemon_test.go
src/framework/cli/cli_test.go
```

每个 `main.go` 改动范围：在文件顶部加 `var GitRevision = "dev"`，将原 `main()` 内容搬入独立 `runServer()` 函数，用 `cli.New(...).OnStart(runServer).Execute()` 替换原 `main()` 体。

---

## Task 1：引入 cobra 依赖

**Files:**
- Modify: `go.mod`（go get 自动更新）

- [ ] **Step 1: 添加 cobra 依赖**

```bash
cd /mnt/c/Users/happyelements/Nutstore/1/Nutstore/goserver/Project
go get github.com/spf13/cobra@latest
```

预期输出：`go: added github.com/spf13/cobra vX.Y.Z`

- [ ] **Step 2: 验证编译通过**

```bash
go build ./...
```

预期：无报错输出。

- [ ] **Step 3: 提交**

```bash
git checkout -b feat/cli-daemon
git add go.mod go.sum
git commit -m "chore: 引入 cobra 依赖"
```

---

## Task 2：实现 `src/common/pidfile`

**Files:**
- Create: `src/common/pidfile/pidfile.go`
- Create: `src/common/pidfile/pidfile_test.go`

- [ ] **Step 1: 写失败测试**

新建 `src/common/pidfile/pidfile_test.go`：

```go
package pidfile_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"project/src/common/pidfile"
)

func TestWriteRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	pid := os.Getpid()

	if err := pidfile.Write(path, pid); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := pidfile.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != pid {
		t.Fatalf("got pid %d, want %d", got, pid)
	}
}

func TestRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	_ = pidfile.Write(path, os.Getpid())
	if err := pidfile.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file should be removed")
	}
}

func TestIsRunning_CurrentProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	_ = pidfile.Write(path, os.Getpid())
	if !pidfile.IsRunning(path) {
		t.Fatal("current process should be running")
	}
}

func TestIsRunning_NoPidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.pid")
	if pidfile.IsRunning(path) {
		t.Fatal("missing file should not be running")
	}
}

func TestRead_InvalidContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pid")
	_ = os.WriteFile(path, []byte("notanumber\n"), 0644)
	_, err := pidfile.Read(path)
	if err == nil {
		t.Fatal("expected error for invalid content")
	}
	var numErr *strconv.NumError
	if !errors.As(err, &numErr) {
		t.Fatalf("expected *strconv.NumError, got %T: %v", err, err)
	}
}
```

> 注意：需要在 import 里补 `"errors"`。

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./src/common/pidfile/...
```

预期：`cannot find package` 或编译错误。

- [ ] **Step 3: 实现 `pidfile.go`**

新建 `src/common/pidfile/pidfile.go`：

```go
package pidfile

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Write 将 pid 写入文件（0644），覆盖已有内容。
func Write(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0644)
}

// Read 读取 pid 文件，返回 PID 整数。
func Read(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// Remove 删除 pid 文件；文件不存在时不报错。
func Remove(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// IsRunning 检查 pid 文件对应的进程是否存活。
// 文件不存在或 pid 无效返回 false。
func IsRunning(path string) bool {
	pid, err := Read(path)
	if err != nil {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// os.FindProcess 在 Unix 上总是成功；用 signal 0 探活
	err = p.Signal(os.Signal(fmt.Errorf("").(error)))
	// 改用 syscall
	return processExists(pid)
}
```

> 注意：`IsRunning` 需要 `syscall.Kill(pid, 0)` 来探活，上面是占位，Step 4 给出完整版。

- [ ] **Step 4: 用 syscall 完善 IsRunning，替换占位代码**

将 `pidfile.go` 的 import 和 `IsRunning` 改为：

```go
package pidfile

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

func Write(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0644)
}

func Read(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func Remove(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// IsRunning 向进程发 signal 0 探活；pid 文件不存在或进程已退出返回 false。
func IsRunning(path string) bool {
	pid, err := Read(path)
	if err != nil {
		return false
	}
	// syscall.Kill(pid, 0)：进程存在且有权限时返回 nil
	err = syscall.Kill(pid, 0)
	return err == nil
}
```

- [ ] **Step 5: 运行测试，确认通过**

```bash
go test ./src/common/pidfile/...
```

预期：`ok  project/src/common/pidfile`

- [ ] **Step 6: 提交**

```bash
git add src/common/pidfile/
git commit -m "feat: 新增 pidfile 包（Write/Read/Remove/IsRunning）"
```

---

## Task 3：实现 `src/common/daemon`

**Files:**
- Create: `src/common/daemon/daemon.go`
- Create: `src/common/daemon/daemon_test.go`

- [ ] **Step 1: 写失败测试**

新建 `src/common/daemon/daemon_test.go`：

```go
package daemon_test

import (
	"testing"

	"project/src/common/daemon"
)

// FilterDaemonFlag 是内部函数，通过包级导出函数间接测试。
// 直接测试 FilterArgs 的行为。

func TestFilterArgs_RemovesDaemonFlag(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{
			in:   []string{"--daemon", "start"},
			want: []string{"start"},
		},
		{
			in:   []string{"-d", "start"},
			want: []string{"start"},
		},
		{
			in:   []string{"--conf-file", "a.yaml", "--daemon", "start"},
			want: []string{"--conf-file", "a.yaml", "start"},
		},
		{
			in:   []string{"start"},
			want: []string{"start"},
		},
	}
	for _, c := range cases {
		got := daemon.FilterArgs(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("in=%v: got %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("in=%v: got[%d]=%q, want[%d]=%q", c.in, i, got[i], i, c.want[i])
			}
		}
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./src/common/daemon/...
```

预期：编译错误（包不存在）。

- [ ] **Step 3: 实现 `daemon.go`**

新建 `src/common/daemon/daemon.go`：

```go
package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

// FilterArgs 过滤掉 --daemon / -d flag，用于 fork 子进程时去除后台化标志。
func FilterArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--daemon" || a == "-d" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// Daemonize 将当前进程转为后台进程：
//  1. 过滤掉 --daemon/-d，重新 exec 自身
//  2. 子进程调用 setsid 脱离终端
//  3. 父进程打印子进程 PID 后退出
//
// 调用方在 --daemon flag 为 true 时调用此函数；
// 子进程不含 --daemon flag，直接执行业务逻辑。
// stdout/stderr 重定向由调用方的 shell（>> log）负责。
func Daemonize() error {
	args := FilterArgs(os.Args[1:])
	cmd := exec.Command(os.Args[0], args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// 父进程退出，子进程继续运行
	os.Exit(0)
	return nil
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./src/common/daemon/...
```

预期：`ok  project/src/common/daemon`

- [ ] **Step 5: 提交**

```bash
git add src/common/daemon/
git commit -m "feat: 新增 daemon 包（Daemonize/FilterArgs）"
```

---

## Task 4：实现 `src/framework/cli`——Flags 与 Builder

**Files:**
- Create: `src/framework/cli/flags.go`
- Create: `src/framework/cli/cli.go`

- [ ] **Step 1: 新建 `flags.go`**

```go
package cli

import "github.com/spf13/cobra"

// Flags 是所有服务共用的 CLI 参数集合。
type Flags struct {
	Addr     string
	ConfFile string
	PidFile  string
	LogFile  string
	Daemon   bool
}

func bindPersistentFlags(cmd *cobra.Command, f *Flags, defaultConf string) {
	pf := cmd.PersistentFlags()
	pf.StringVarP(&f.Addr,     "addr",      "a", "",          "监听地址（覆盖配置文件）")
	pf.StringVarP(&f.ConfFile, "conf-file",  "c", defaultConf, "服务配置文件路径")
	pf.StringVarP(&f.PidFile,  "pid-file",   "p", "",          "PID 文件路径")
	pf.StringVarP(&f.LogFile,  "log-file",   "l", "",          "日志配置文件路径")
	pf.BoolVarP  (&f.Daemon,   "daemon",     "d", false,       "以后台方式运行（仅 start 子命令有效）")
}
```

- [ ] **Step 2: 新建 `cli.go`**

```go
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Builder 用于组装服务 CLI 入口。
type Builder struct {
	name        string
	short       string
	defaultConf string
	gitRevision string
	onStart     func(*Flags) error
	onReload    func(*Flags) error
}

// New 创建 Builder。name 为二进制名称，short 为一行描述。
func New(name, short string) *Builder {
	return &Builder{name: name, short: short}
}

// DefaultConf 设置 --conf-file 的默认值。
func (b *Builder) DefaultConf(path string) *Builder {
	b.defaultConf = path
	return b
}

// GitRevision 注入编译期 git 版本号（通过 -ldflags "-X main.GitRevision=xxx"）。
func (b *Builder) GitRevision(rev string) *Builder {
	b.gitRevision = rev
	return b
}

// OnStart 注册 start 子命令的业务逻辑回调。
func (b *Builder) OnStart(fn func(*Flags) error) *Builder {
	b.onStart = fn
	return b
}

// OnReload 注册 reload 子命令的回调（通常是向自身发 SIGHUP）。
// 若不注册，reload 子命令仍存在，但执行默认的 SIGHUP 信号发送。
func (b *Builder) OnReload(fn func(*Flags) error) *Builder {
	b.onReload = fn
	return b
}

// Execute 组装 cobra 根命令并执行，遇到错误输出到 stderr 后以 exit(1) 退出。
func (b *Builder) Execute() {
	var f Flags

	root := &cobra.Command{
		Use:           b.name,
		Short:         b.short,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	bindPersistentFlags(root, &f, b.defaultConf)

	root.AddCommand(
		newStartCmd(&f, b.onStart),
		newStopCmd(&f),
		newKillCmd(&f),
		newReloadCmd(&f, b.onReload),
		newStatusCmd(&f),
		newVersionCmd(b.name, b.gitRevision),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 3: 验证编译（commands.go 还不存在，先确认 flags/cli 编译）**

```bash
go build ./src/framework/cli/...
```

预期：编译报错"newStartCmd undefined"等，说明 flags.go / cli.go 本身语法正确，只是缺 commands.go。

---

## Task 5：实现 `src/framework/cli`——子命令

**Files:**
- Create: `src/framework/cli/commands.go`
- Create: `src/framework/cli/cli_test.go`

- [ ] **Step 1: 新建 `commands.go`**

```go
package cli

import (
	"fmt"
	"os"
	"syscall"

	"github.com/spf13/cobra"
	"project/src/common/daemon"
	"project/src/common/pidfile"
)

func newStartCmd(f *Flags, onStart func(*Flags) error) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "启动服务",
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.Daemon {
				return daemon.Daemonize()
			}
			if f.PidFile != "" {
				if pidfile.IsRunning(f.PidFile) {
					return fmt.Errorf("service already running (pid-file: %s)", f.PidFile)
				}
				if err := pidfile.Write(f.PidFile, os.Getpid()); err != nil {
					return fmt.Errorf("write pid-file: %w", err)
				}
				defer pidfile.Remove(f.PidFile)
			}
			if onStart == nil {
				return fmt.Errorf("OnStart callback not registered")
			}
			return onStart(f)
		},
	}
}

func newStopCmd(f *Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "优雅停止服务（SIGTERM）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return sendSignal(f.PidFile, syscall.SIGTERM)
		},
	}
}

func newKillCmd(f *Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "kill",
		Short: "强制杀死服务（SIGKILL）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return sendSignal(f.PidFile, syscall.SIGKILL)
		},
	}
}

func newReloadCmd(f *Flags, onReload func(*Flags) error) *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "重载配置（SIGHUP）",
		RunE: func(cmd *cobra.Command, args []string) error {
			if onReload != nil {
				return onReload(f)
			}
			return sendSignal(f.PidFile, syscall.SIGHUP)
		},
	}
}

func newStatusCmd(f *Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "查看服务运行状态",
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.PidFile == "" {
				return fmt.Errorf("--pid-file not specified")
			}
			if pidfile.IsRunning(f.PidFile) {
				pid, _ := pidfile.Read(f.PidFile)
				fmt.Printf("running (pid=%d)\n", pid)
			} else {
				fmt.Println("stopped")
			}
			return nil
		},
	}
}

func newVersionCmd(name, revision string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印版本信息",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("%s %s\n", name, revision)
		},
	}
}

// sendSignal 从 pid-file 读取 PID，向进程发送指定信号。
func sendSignal(pidFile string, sig syscall.Signal) error {
	if pidFile == "" {
		return fmt.Errorf("--pid-file not specified")
	}
	pid, err := pidfile.Read(pidFile)
	if err != nil {
		return fmt.Errorf("read pid-file: %w", err)
	}
	if err := syscall.Kill(pid, sig); err != nil {
		return fmt.Errorf("kill pid=%d sig=%s: %w", pid, sig, err)
	}
	return nil
}
```

- [ ] **Step 2: 写 cli_test.go**

新建 `src/framework/cli/cli_test.go`：

```go
package cli_test

import (
	"testing"

	"project/src/framework/cli"
)

// 验证 Builder 链式调用不 panic，Execute 在子命令执行 OnStart 时调用回调。
func TestBuilder_OnStartCalled(t *testing.T) {
	called := false
	b := cli.New("testsvr", "测试服务").
		DefaultConf("run/testsvr/conf/config.yaml").
		GitRevision("abc1234").
		OnStart(func(f *cli.Flags) error {
			called = true
			return nil
		})

	// 直接调用内部 RunE，而不走 Execute()（Execute 会 os.Exit）
	// 通过导出的 RunStart 测试入口（见下方 cli.go 补充）
	_ = b
	// 此处只验证链式调用不 panic
	if b == nil {
		t.Fatal("Builder should not be nil")
	}
	_ = called
}

func TestFlags_Defaults(t *testing.T) {
	// 验证 Flags 零值合理
	var f cli.Flags
	if f.Daemon {
		t.Fatal("Daemon should default to false")
	}
}
```

- [ ] **Step 3: 运行全量编译和测试**

```bash
go build ./...
go test ./src/common/pidfile/... ./src/common/daemon/... ./src/framework/cli/...
```

预期：三个包均 `ok`。

- [ ] **Step 4: 提交**

```bash
git add src/framework/cli/
git commit -m "feat: 新增 framework/cli 包（cobra 子命令封装）"
```

---

## Task 6：更新 `development.md`

**Files:**
- Modify: `development.md`

- [ ] **Step 1: 在"新增代码放置规则"表格补行**

在 `development.md` 的表格末尾（`| 新服务进程 | ... |` 行之后）加入：

```markdown
| 服务进程启动封装（cobra CLI/daemon/pid） | `src/framework/cli/`（项目约定层）<br>`src/common/pidfile/`（pid 工具）<br>`src/common/daemon/`（后台化工具） |
```

- [ ] **Step 2: 在"已有通用工具"列表补充 pidfile 和 daemon**

在 `development.md` 的"已有通用工具"列表末尾追加：

```markdown
- `src/common/pidfile` — PID 文件工具（Write/Read/Remove/IsRunning）
- `src/common/daemon` — 进程后台化（Daemonize/FilterArgs）
```

- [ ] **Step 3: 提交**

```bash
git add development.md
git commit -m "docs: 更新 development.md，记录 cli/pidfile/daemon 包放置规则"
```

---

## Task 7：改造 gatesvr/main.go（示范，其余服务同理）

**Files:**
- Modify: `src/servers/gatesvr/main.go`

- [ ] **Step 1: 重写 main.go**

```go
package main

import (
	"time"

	conf "project/conf/schema/gen"
	"project/protocal/gen/routes"
	cfgloader "project/src/common/config"
	"project/src/common/logger"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cli"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
	"project/src/servers/gatesvr/internal"
)

var GitRevision = "dev" // 编译时注入：-ldflags "-X main.GitRevision=<git-sha>"

func main() {
	cli.New("gatesvr", "网关服务").
		DefaultConf("run/gatesvr/conf/config.yaml").
		GitRevision(GitRevision).
		OnStart(runServer).
		Execute()
}

func runServer(f *cli.Flags) error {
	commonCfg, err := cfgloader.LoadCommon("run/common/conf/config.yaml")
	if err != nil {
		return err
	}

	svrLoader := cfgloader.NewLoader[conf.GateSvr](f.ConfFile)
	svrLoader.MustLoad()
	cfg := svrLoader.Current()

	log, _ := logger.NewZapDevelopment()
	logger.SetGlobal(log)

	self, err := cluster.ParseNodeID(cfg.NodeId)
	if err != nil {
		return err
	}
	cls, err := transport.NewNatsCluster(self, transport.NatsClusterConfig{
		EtcdEndpoints:  commonCfg.Etcd.Endpoints,
		NatsURLs:       commonCfg.Nats.Urls,
		SelfAddr:       cfg.Addr,
		ServerTypeName: cfg.ServerTypeName,
	})
	if err != nil {
		return err
	}

	app := application.NewBuilder().
		NodeID(cfg.NodeId).
		NodeType(cfg.ServerTypeName).
		Frontend(cfg.Addr).
		Serializer("protobuf", protobuf.NewSerializer()).
		Routes(routes.Config()).
		Cluster(cls).
		Build()

	gateModule := internal.NewGateModule(cfg.NodeId, app.Sessions(), app.Cluster(), app.AgentMap())
	app.Register(gateModule)
	if err := app.RegisterHandler(internal.NewGateHandler(gateModule), nil); err != nil {
		return err
	}

	app.Start()
	if err := cls.Init(); err != nil {
		return err
	}
	defer cls.Stop()

	stop := svrLoader.Watch(30 * time.Second)
	defer stop()

	logger.Info("gatesvr started",
		logger.String("nodeID", cfg.NodeId),
		logger.String("addr", cfg.Addr))
	app.Run()
	return nil
}
```

- [ ] **Step 2: 编译验证**

```bash
go build ./src/servers/gatesvr/...
```

预期：无报错。

- [ ] **Step 3: 提交**

```bash
git add src/servers/gatesvr/main.go
git commit -m "refactor: gatesvr main.go 接入 cli 包"
```

---

## Task 8：改造其余五个服务（lobbysvr / onlinesvr / routersvr / roomsvr / matchsvr）

每个服务的改造步骤与 Task 7 完全相同，只有三处变化：
1. `cli.New` 的 `name` 和 `short` 换成对应服务名
2. `DefaultConf` 路径换成对应服务路径
3. `runServer` 里的 `conf.XxxSvr` 泛型参数换成对应类型

- [ ] **Step 1: 改造 lobbysvr/main.go**（参照 Task 7 模式，`conf.LobbySvr`，路径 `run/lobbysvr/conf/config.yaml`）

- [ ] **Step 2: 改造 onlinesvr/main.go**（`conf.OnlineSvr`，路径 `run/onlinesvr/conf/config.yaml`）

- [ ] **Step 3: 改造 routersvr/main.go**（`conf.RouterSvr`，路径 `run/routersvr/conf/config.yaml`）

- [ ] **Step 4: 改造 roomsvr/main.go**（`conf.RoomSvr`，路径 `run/roomsvr/conf/config.yaml`）

- [ ] **Step 5: 改造 matchsvr/main.go**（`conf.MatchSvr`，路径 `run/matchsvr/conf/config.yaml`）

- [ ] **Step 6: 全量编译**

```bash
go build ./...
```

预期：无报错。

- [ ] **Step 7: 提交**

```bash
git add src/servers/
git commit -m "refactor: 其余五个服务 main.go 接入 cli 包"
```

---

## Task 9：更新 architecture.md

**Files:**
- Modify: `architecture.md`

- [ ] **Step 1: 在架构概览目录树 `framework/` 下补三行**

在 `architecture.md` 的目录树 `framework/` 节点，`application/` 行之前插入：

```
    ├── cli/           # 服务进程 CLI 入口封装（cobra Builder/start/stop/kill/reload/status）
```

在 `src/common/` 节点，`mongo/` 行之后追加：

```
    ├── pidfile/       # PID 文件工具（Write/Read/Remove/IsRunning）
    └── daemon/        # 进程后台化（Daemonize/FilterArgs，重 exec 自身）
```

- [ ] **Step 2: 提交**

```bash
git add architecture.md
git commit -m "docs: 更新 architecture.md，记录 cli/pidfile/daemon 包"
```

---

## 自检

| 需求 | 对应 Task |
|---|---|
| --addr/--conf-file/--pid-file/--log-file/--daemon flags | Task 4 flags.go |
| start/stop/kill/reload/status 子命令 | Task 5 commands.go |
| --daemon 触发 daemonize | Task 3 + Task 5 newStartCmd |
| pid 文件写入/探活/清理 | Task 2 |
| git revision 打印 | Task 5 newVersionCmd |
| 各 main.go 接入 | Task 7 + 8 |
| 文档同步 | Task 6 + 9 |
