# Server Bootstrap Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor service startup from `bootstrap.New + Module` entrypoints to Server-owned `Options → Main(opt) → NewServer(opt) → Init → Run → Fini` with generic cobra command wiring.

**Architecture:** `internal/core/app` owns lifecycle, module containers, and pure parameter structs. `internal/core/cmd` owns cobra glue and generic `Command[T]`. Each service package owns `Options`, `NewOptions`, `Main(opt)`, and `NewXxxServer(opt)`; `cmd/xxxsvr/main.go` only wires command metadata and optional service-specific flags.

**Tech Stack:** Go 1.26, cobra/pflag, YAML config via `pkg/configgen`, existing `project/internal/core/*` modules.

## Global Constraints

- Go module name is `project`; imports must use `project/...`.
- Go version is `1.26` from `go.mod`.
- Config is YAML loaded by existing configgen/config module; do not introduce viper.
- `internal/core/app` must not import cobra or pflag.
- cobra/pflag glue belongs in `internal/core/cmd` and `cmd/xxxsvr/main.go` only.
- No reflection in command/options construction; every service provides `NewOptions()`.
- No `Server` interface; services embed `*app.App` and inherit `Init()`, `Run()`, `Fini()`.
- Rename Module lifecycle methods: `OnAfterInit → AfterInit`, `OnBeforeStop → BeforeStop`, `OnStop → Fini`.
- Delete `internal/core/bootstrap` and `internal/core/app/option.go` after all callers migrate.
- Preserve user work in `tools/e2e_full/*`; these files are already modified/untracked in the working tree, so use targeted edits and do not overwrite file contents wholesale.

---

## File Structure

### Core files

- Create `internal/core/app/options.go`
  - Defines `BaseOptions`, `Options` interface, `CommandMeta`, `ValidateConfig`.
  - Keeps app package free of cobra/pflag.
- Modify `internal/core/app/module.go`
  - Rename lifecycle method names.
  - Keep `BaseModule`, `ReadyWaiter`, `Updater`.
- Modify `internal/core/app/app.go`
  - Remove `frameworkModules`.
  - Change `New` to accept `*BaseOptions` and remain a pure lifecycle container.
  - Split lifecycle into `Init()`, `Run()`, `Fini()`.
- Create `internal/core/runtime/app.go`
  - Provides `NewApp(opt *app.BaseOptions) *app.App` and wires config/log/ragent infra modules above the app package to avoid import cycles.
- Delete `internal/core/app/option.go`
  - `Tick`, `ReadyTimeout`, `DrainTimeout` move into `BaseOptions`.
- Create `internal/core/cmd/command.go`
  - Defines generic `Command[T app.Options]`, `BindCommonFlags`, `AddVersionCmd`, version vars.
- Delete `internal/core/bootstrap/bootstrap.go` and `internal/core/bootstrap/cobra.go`
  - Functionality moves to `runtime` and `corecmd`.

### Service package files

For each service package:

- Create `internal/gatesvr/options.go`
- Create `internal/lobbysvr/options.go`
- Create `internal/roomsvr/options.go`
- Create `internal/matchsvr/options.go`
- Create `internal/onlinesvr/options.go`
- Create `internal/routeragent/options.go`

Each defines `Options`, `NewOptions()`, `Base()`, `Defaults()`.

For each service package:

- Rename or replace `internal/<svc>/module.go` with `internal/<svc>/<svc-without-svr?>server.go` where practical.
- The package still may contain a `Module` type internally, but each package must expose:
  - `type XxxServer struct { *app.App }`
  - `func Main(opt *Options) error`
  - `func NewXxxServer(opt *Options) *XxxServer`

Expected server names:

| Package | Server type | Constructor |
|---|---|---|
| `gatesvr` | `GateServer` | `NewGateServer` |
| `lobbysvr` | `LobbyServer` | `NewLobbyServer` |
| `roomsvr` | `RoomServer` | `NewRoomServer` |
| `matchsvr` | `MatchServer` | `NewMatchServer` |
| `onlinesvr` | `OnlineServer` | `NewOnlineServer` |
| `routeragent` | `RouterAgentServer` | `NewRouterAgentServer` |

### Command entrypoints

- Modify `cmd/gatesvr/main.go`
- Modify `cmd/lobbysvr/main.go`
- Modify `cmd/roomsvr/main.go`
- Modify `cmd/matchsvr/main.go`
- Modify `cmd/onlinesvr/main.go`
- Modify `cmd/routeragent/main.go`

All use `corecmd.Command[T]`. Only `gatesvr` has service-specific `addFlags` for `--listen-addr`.

### Tests and references

- Create `internal/core/app/app_test.go`
- Create `internal/core/cmd/command_test.go`
- Modify existing tests that call old lifecycle names:
  - `internal/core/config/module_test.go`
  - `internal/core/log/module_test.go`
  - `internal/core/db/db_test.go`
  - `tools/e2e_full/*.go` targeted lifecycle method rename only
- Modify code references found by:
  - `rg "OnAfterInit|OnBeforeStop|OnStop|bootstrap\.|app\.With" -g'*.go'`

---

### Task 1: Add Options And Command Infrastructure

**Files:**
- Create: `internal/core/app/options.go`
- Create: `internal/core/cmd/command.go`
- Create: `internal/core/cmd/command_test.go`

**Interfaces:**
- Produces: `app.BaseOptions`, `app.Options`, `app.CommandMeta`, `app.ValidateConfig(configFiles []string) error`
- Produces: `corecmd.Command[T app.Options](meta app.CommandMeta, newOptions func() T, addFlags func(*cobra.Command, T), run func(T) error) *cobra.Command`
- Produces: `corecmd.BindCommonFlags(cmd *cobra.Command, opt *app.BaseOptions, defaultConfs []string)`
- Produces: `corecmd.AddVersionCmd(root *cobra.Command)`

- [ ] **Step 1: Write command tests first**

Create `internal/core/cmd/command_test.go`:

```go
package cmd

import (
	"testing"
	"time"

	"github.com/spf13/cobra"

	"project/internal/core/app"
)

type testOptions struct {
	app.BaseOptions
	ListenAddr string
}

func newTestOptions() *testOptions {
	opt := &testOptions{}
	opt.Defaults()
	opt.ListenAddr = "127.0.0.1:7001"
	return opt
}

func (o *testOptions) Base() *app.BaseOptions { return &o.BaseOptions }
func (o *testOptions) Defaults() {
	o.BaseOptions.Defaults()
	o.ListenAddr = "127.0.0.1:7001"
}

func TestCommandBindsCommonAndExtraFlags(t *testing.T) {
	var got *testOptions
	cmd := Command[*testOptions](
		app.CommandMeta{Use: "testsvr", Short: "test server", Confs: []string{"run/test/conf/"}},
		newTestOptions,
		func(cmd *cobra.Command, opt *testOptions) {
			cmd.Flags().StringVar(&opt.ListenAddr, "listen-addr", opt.ListenAddr, "listen address")
		},
		func(opt *testOptions) error {
			got = opt
			return nil
		},
	)
	cmd.SetArgs([]string{"-c", "run/common/conf/", "-c", "run/test/conf/", "--server-index", "7", "--listen-addr", "0.0.0.0:9000"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got == nil {
		t.Fatalf("run callback was not called")
	}
	if want := []string{"run/common/conf/", "run/test/conf/"}; len(got.ConfigFiles) != len(want) || got.ConfigFiles[0] != want[0] || got.ConfigFiles[1] != want[1] {
		t.Fatalf("ConfigFiles = %#v, want %#v", got.ConfigFiles, want)
	}
	if got.ServerIndex != 7 {
		t.Fatalf("ServerIndex = %d, want 7", got.ServerIndex)
	}
	if got.ListenAddr != "0.0.0.0:9000" {
		t.Fatalf("ListenAddr = %q", got.ListenAddr)
	}
	if got.ReadyTimeout != 10*time.Second {
		t.Fatalf("ReadyTimeout = %s, want 10s", got.ReadyTimeout)
	}
}

func TestCommandVersionSubcommandDoesNotRunMain(t *testing.T) {
	ran := false
	cmd := Command[*testOptions](
		app.CommandMeta{Use: "testsvr", Short: "test server", Confs: []string{"run/test/conf/"}},
		newTestOptions,
		nil,
		func(opt *testOptions) error {
			ran = true
			return nil
		},
	)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute version: %v", err)
	}
	if ran {
		t.Fatalf("main run callback should not run for version subcommand")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/core/cmd`

Expected: FAIL with package/file not found or undefined `Command`, `app.BaseOptions`, `app.CommandMeta`.

- [ ] **Step 3: Create app options**

Create `internal/core/app/options.go`:

```go
package app

import (
	"fmt"
	"time"

	"project/pkg/configgen"
)

// Options is implemented by each service's startup options struct.
type Options interface {
	Base() *BaseOptions
	Defaults()
}

// BaseOptions contains startup parameters shared by all services.
type BaseOptions struct {
	ConfigFiles  []string
	ServerIndex  int32
	ValidateOnly bool

	Tick         time.Duration
	ReadyTimeout time.Duration
	DrainTimeout time.Duration
}

func (o *BaseOptions) Base() *BaseOptions { return o }

func (o *BaseOptions) Defaults() {
	o.ServerIndex = -1
	o.ReadyTimeout = 10 * time.Second
}

// CommandMeta describes one service command.
type CommandMeta struct {
	Use   string
	Short string
	Confs []string
}

// ValidateConfig verifies that config files can be loaded.
func ValidateConfig(configFiles []string) error {
	if len(configFiles) == 0 {
		return fmt.Errorf("config: no config files specified")
	}
	_, err := configgen.LoadFiles[map[string]any](configFiles...)
	if err != nil {
		return fmt.Errorf("config validate: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Create core command package**

Create `internal/core/cmd/command.go`:

```go
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"project/internal/core/app"
	"project/internal/core/config"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func Command[T app.Options](
	meta app.CommandMeta,
	newOptions func() T,
	addFlags func(cmd *cobra.Command, opt T),
	run func(opt T) error,
) *cobra.Command {
	opt := newOptions()
	opt.Base().ConfigFiles = append([]string(nil), meta.Confs...)

	root := &cobra.Command{
		Use:          meta.Use,
		Short:        meta.Short,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			base := opt.Base()
			if base.ValidateOnly {
				return app.ValidateConfig(base.ConfigFiles)
			}
			if cmd.Flags().Changed("server-index") && base.ServerIndex >= 0 {
				config.OverrideServerIndex(uint32(base.ServerIndex))
			}
			return run(opt)
		},
	}

	BindCommonFlags(root, opt.Base(), meta.Confs)
	if addFlags != nil {
		addFlags(root, opt)
	}
	AddVersionCmd(root)
	return root
}

func BindCommonFlags(root *cobra.Command, opt *app.BaseOptions, defaultConfs []string) {
	f := root.Flags()
	f.StringArrayVarP(&opt.ConfigFiles, "config", "c", append([]string(nil), defaultConfs...), "config file(s), can specify multiple times")
	f.Int32Var(&opt.ServerIndex, "server-index", -1, "覆盖 node.server_index（横向扩容多副本）")
	f.BoolVar(&opt.ValidateOnly, "validate-config", false, "校验配置后退出（CI 用）")
}

func AddVersionCmd(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "打印版本信息并退出",
		Run: func(*cobra.Command, []string) {
			fmt.Printf("%s version %s (built %s)\n", root.Use, Version, BuildTime)
		},
	})
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/core/cmd`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/core/app/options.go internal/core/cmd/command.go internal/core/cmd/command_test.go
git commit -m "feat: add generic service command options"
```

---

### Task 2: Refactor App Lifecycle And Module Hooks

**Files:**
- Modify: `internal/core/app/app.go`
- Modify: `internal/core/app/module.go`
- Delete: `internal/core/app/option.go`
- Create: `internal/core/app/app_test.go`
- Create: `internal/core/runtime/app.go`
- Modify hook names in all `internal/**/*.go` and targeted `tools/e2e_full/*.go`

**Interfaces:**
- Consumes: `app.BaseOptions` from Task 1.
- Produces: `app.New(opt *BaseOptions) *App`
- Produces: `(*App).Init() error`, `(*App).Run() error`, `(*App).Fini()`
- Produces: `Module.AfterInit()`, `Module.BeforeStop()`, `Module.Fini()`

- [ ] **Step 1: Write App lifecycle test**

Create `internal/core/app/app_test.go`:

```go
package app

import (
	"reflect"
	"testing"
	"time"
)

type lifecycleModule struct {
	name   string
	events *[]string
	BaseModule
}

func (m *lifecycleModule) Init(*App) error {
	*m.events = append(*m.events, m.name+":Init")
	return nil
}

func (m *lifecycleModule) AfterInit() error {
	*m.events = append(*m.events, m.name+":AfterInit")
	return nil
}

func (m *lifecycleModule) BeforeStop() {
	*m.events = append(*m.events, m.name+":BeforeStop")
}

func (m *lifecycleModule) Fini() {
	*m.events = append(*m.events, m.name+":Fini")
}

func TestAppInitAndFiniLifecycleOrder(t *testing.T) {
	var events []string
	opt := &BaseOptions{}
	opt.Defaults()
	a := New(opt)
	a.Register(&lifecycleModule{name: "m1", events: &events})
	a.Register(&lifecycleModule{name: "m2", events: &events})

	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	a.Fini()

	want := []string{
		"m1:Init", "m2:Init",
		"m1:AfterInit", "m2:AfterInit",
		"m2:BeforeStop", "m1:BeforeStop",
		"m2:Fini", "m1:Fini",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestAppRunDrivesUpdaterUntilFini(t *testing.T) {
	opt := &BaseOptions{Tick: time.Millisecond}
	opt.Defaults()
	a := New(opt)
	updates := 0
	a.Register(UpdaterFunc(func(time.Duration) {
		updates++
		if updates == 2 {
			a.Fini()
		}
	}))

	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := a.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if updates < 2 {
		t.Fatalf("updates = %d, want at least 2", updates)
	}
}

type UpdaterFunc func(time.Duration)

func (f UpdaterFunc) Init(*App) error    { return nil }
func (f UpdaterFunc) AfterInit() error   { return nil }
func (f UpdaterFunc) BeforeStop()        {}
func (f UpdaterFunc) Fini()              {}
func (f UpdaterFunc) Update(d time.Duration) { f(d) }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/app`

Expected: FAIL because `New` currently expects `infra ...Module` and Module still has `OnAfterInit`, `OnBeforeStop`, `OnStop`.

- [ ] **Step 3: Update Module interface**

Replace `internal/core/app/module.go` with:

```go
package app

import (
	"context"
	"time"
)

// Module is a lifecycle unit managed by App.
type Module interface {
	Init(a *App) error
	AfterInit() error
	BeforeStop()
	Fini()
}

// BaseModule provides no-op lifecycle methods.
type BaseModule struct{}

func (b *BaseModule) Init(*App) error  { return nil }
func (b *BaseModule) AfterInit() error { return nil }
func (b *BaseModule) BeforeStop()      {}
func (b *BaseModule) Fini()            {}

// Updater is driven by App's main loop tick.
type Updater interface {
	Update(dt time.Duration)
}

// ReadyWaiter waits for asynchronous module readiness.
type ReadyWaiter interface {
	WaitReady(ctx context.Context) error
}
```

- [ ] **Step 4: Replace App implementation**

Replace `internal/core/app/app.go` with:

```go
package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"project/internal/core/config"
	corelog "project/internal/core/log"
	"project/internal/core/ragent"
	"project/pkg/taskqueue"
)

// App owns module lifecycle and the single-threaded main loop.
type App struct {
	infraModules []Module
	modules      []Module
	q            *taskqueue.Queue
	tick         time.Duration
	readyTimeout time.Duration
	drainTimeout time.Duration
	quit         chan struct{}
	stopped      chan struct{}
	stopOnce     sync.Once
}

func New(opt *BaseOptions) *App {
	if opt == nil {
		opt = &BaseOptions{}
		opt.Defaults()
	}
	return &App{
		infraModules: []Module{
			config.NewModule(opt.ConfigFiles),
			corelog.NewModule(),
			ragent.NewModule(),
		},
		q:            taskqueue.New(0),
		tick:         opt.Tick,
		readyTimeout: opt.ReadyTimeout,
		drainTimeout: opt.DrainTimeout,
		quit:         make(chan struct{}),
		stopped:      make(chan struct{}),
	}
}

func (a *App) Register(m Module) { a.modules = append(a.modules, m) }

func (a *App) Post(fn func()) { a.q.Post(fn) }

func GetModule[T Module](a *App) T {
	for _, m := range a.infraModules {
		if v, ok := m.(T); ok {
			return v
		}
	}
	for _, m := range a.modules {
		if v, ok := m.(T); ok {
			return v
		}
	}
	var zero T
	panic(fmt.Sprintf("app.GetModule: module %T not found", zero))
}

func (a *App) Init() error {
	all := a.allModules()
	for _, m := range all {
		if err := m.Init(a); err != nil {
			return fmt.Errorf("module %T Init: %w", m, err)
		}
	}
	for _, m := range all {
		if err := m.AfterInit(); err != nil {
			return fmt.Errorf("module %T AfterInit: %w", m, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), a.readyTimeout)
	defer cancel()
	for _, m := range all {
		if w, ok := m.(ReadyWaiter); ok {
			if err := w.WaitReady(ctx); err != nil {
				return fmt.Errorf("module %T WaitReady: %w", m, err)
			}
		}
	}
	return nil
}

func (a *App) Run() error {
	defer a.Fini()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		select {
		case <-sigCh:
			if a.drainTimeout > 0 {
				time.Sleep(a.drainTimeout)
			}
			a.closeQuit()
		case <-a.stopped:
		}
	}()

	a.runLoop(a.allModules())
	return nil
}

func (a *App) Fini() {
	a.closeQuit()
	a.stopOnce.Do(func() {
		all := a.allModules()
		for i := len(all) - 1; i >= 0; i-- {
			all[i].BeforeStop()
		}
		for i := len(all) - 1; i >= 0; i-- {
			all[i].Fini()
		}
		close(a.stopped)
	})
}

func (a *App) closeQuit() {
	select {
	case <-a.quit:
	default:
		close(a.quit)
	}
}

func (a *App) runLoop(modules []Module) {
	var ticker *time.Ticker
	if a.tick > 0 {
		ticker = time.NewTicker(a.tick)
		defer ticker.Stop()
	}

	for {
		if ticker == nil {
			select {
			case fn := <-a.q.C():
				fn()
			case <-a.quit:
				return
			}
			continue
		}

		select {
		case fn := <-a.q.C():
			fn()
		case <-ticker.C:
			for _, m := range modules {
				if u, ok := m.(Updater); ok {
					u.Update(a.tick)
				}
			}
		case <-a.quit:
			return
		}
	}
}

func (a *App) allModules() []Module {
	mods := make([]Module, 0, len(a.infraModules)+len(a.modules))
	mods = append(mods, a.infraModules...)
	mods = append(mods, a.modules...)
	return mods
}

var _ Poster = (*App)(nil)

type Poster interface {
	Post(fn func())
}
```

- [ ] **Step 5: Delete option.go**

Delete `internal/core/app/option.go`.

- [ ] **Step 6: Rename lifecycle methods across source**

Use targeted search and edit, not wholesale overwrites:

```bash
rg "OnAfterInit|OnBeforeStop|OnStop" internal tools/e2e_full -g'*.go'
```

Apply these exact symbol renames in implementation files and tests:

```text
OnAfterInit   → AfterInit
OnBeforeStop  → BeforeStop
OnStop        → Fini
```

Update comments as needed, for example:

```go
// AfterInit 启动 routeragent 子组件
func (m *Module) AfterInit() error { ... }

// BeforeStop 停止后台组件
func (m *Module) BeforeStop() { ... }

func (m *Module) Fini() {}
```

- [ ] **Step 7: Run focused tests**

Run:

```bash
go test ./internal/core/app ./internal/core/config ./internal/core/log ./internal/core/db ./internal/core/ragent ./internal/routeragent
```

Expected: PASS after all lifecycle references are renamed.

- [ ] **Step 8: Commit**

```bash
git add internal/core/app internal/core/config internal/core/log internal/core/db internal/core/ragent internal/routeragent tools/e2e_full
git commit -m "refactor: split app lifecycle into init run fini"
```

---

### Task 3: Add Server Entry Points And Options For Non-Gate Services

**Files:**
- Create: `internal/lobbysvr/options.go`
- Create or replace: `internal/lobbysvr/lobbyserver.go`
- Create: `internal/roomsvr/options.go`
- Create or replace: `internal/roomsvr/roomserver.go`
- Create: `internal/matchsvr/options.go`
- Create or replace: `internal/matchsvr/matchserver.go`
- Create: `internal/onlinesvr/options.go`
- Create or replace: `internal/onlinesvr/onlineserver.go`
- Create: `internal/routeragent/options.go`
- Create or replace: `internal/routeragent/routeragentserver.go`

**Interfaces:**
- Consumes: `app.New(opt *app.BaseOptions) *app.App` from Task 2.
- Produces: `NewOptions() *Options`, `Main(opt *Options) error`, and `NewXxxServer(opt *Options) *XxxServer` for each non-gate service.

- [ ] **Step 1: Add lobbysvr Options and server**

Create `internal/lobbysvr/options.go`:

```go
package lobbysvr

import "project/internal/core/app"

type Options struct{ app.BaseOptions }

func NewOptions() *Options {
	opt := &Options{}
	opt.Defaults()
	return opt
}

func (o *Options) Base() *app.BaseOptions { return &o.BaseOptions }
func (o *Options) Defaults() { o.BaseOptions.Defaults() }
```

Create `internal/lobbysvr/lobbyserver.go`:

```go
package lobbysvr

import "project/internal/core/app"

type LobbyServer struct{ *app.App }

func Main(opt *Options) error {
	s := NewLobbyServer(opt)
	if err := s.Init(); err != nil {
		return err
	}
	return s.Run()
}

func NewLobbyServer(opt *Options) *LobbyServer {
	s := &LobbyServer{App: app.New(&opt.BaseOptions)}
	s.Register(NewModule())
	return s
}
```

- [ ] **Step 2: Add roomsvr Options and server**

Create `internal/roomsvr/options.go`:

```go
package roomsvr

import (
	"time"

	"project/internal/core/app"
)

type Options struct{ app.BaseOptions }

func NewOptions() *Options {
	opt := &Options{}
	opt.Defaults()
	return opt
}

func (o *Options) Base() *app.BaseOptions { return &o.BaseOptions }
func (o *Options) Defaults() {
	o.BaseOptions.Defaults()
	o.Tick = 50 * time.Millisecond
}
```

Create `internal/roomsvr/roomserver.go`:

```go
package roomsvr

import "project/internal/core/app"

type RoomServer struct{ *app.App }

func Main(opt *Options) error {
	s := NewRoomServer(opt)
	if err := s.Init(); err != nil {
		return err
	}
	return s.Run()
}

func NewRoomServer(opt *Options) *RoomServer {
	s := &RoomServer{App: app.New(&opt.BaseOptions)}
	s.Register(NewModule())
	return s
}
```

- [ ] **Step 3: Add matchsvr Options and server**

Create `internal/matchsvr/options.go`:

```go
package matchsvr

import (
	"time"

	"project/internal/core/app"
)

type Options struct{ app.BaseOptions }

func NewOptions() *Options {
	opt := &Options{}
	opt.Defaults()
	return opt
}

func (o *Options) Base() *app.BaseOptions { return &o.BaseOptions }
func (o *Options) Defaults() {
	o.BaseOptions.Defaults()
	o.Tick = 100 * time.Millisecond
}
```

Create `internal/matchsvr/matchserver.go`:

```go
package matchsvr

import "project/internal/core/app"

type MatchServer struct{ *app.App }

func Main(opt *Options) error {
	s := NewMatchServer(opt)
	if err := s.Init(); err != nil {
		return err
	}
	return s.Run()
}

func NewMatchServer(opt *Options) *MatchServer {
	s := &MatchServer{App: app.New(&opt.BaseOptions)}
	s.Register(NewModule())
	return s
}
```

- [ ] **Step 4: Add onlinesvr Options and server**

Create `internal/onlinesvr/options.go`:

```go
package onlinesvr

import (
	"time"

	"project/internal/core/app"
)

type Options struct{ app.BaseOptions }

func NewOptions() *Options {
	opt := &Options{}
	opt.Defaults()
	return opt
}

func (o *Options) Base() *app.BaseOptions { return &o.BaseOptions }
func (o *Options) Defaults() {
	o.BaseOptions.Defaults()
	o.Tick = 100 * time.Millisecond
}
```

Create `internal/onlinesvr/onlineserver.go`:

```go
package onlinesvr

import "project/internal/core/app"

type OnlineServer struct{ *app.App }

func Main(opt *Options) error {
	s := NewOnlineServer(opt)
	if err := s.Init(); err != nil {
		return err
	}
	return s.Run()
}

func NewOnlineServer(opt *Options) *OnlineServer {
	s := &OnlineServer{App: app.New(&opt.BaseOptions)}
	s.Register(NewModule())
	return s
}
```

- [ ] **Step 5: Add routeragent Options and server**

Create `internal/routeragent/options.go`:

```go
package routeragent

import "project/internal/core/app"

type Options struct{ app.BaseOptions }

func NewOptions() *Options {
	opt := &Options{}
	opt.Defaults()
	return opt
}

func (o *Options) Base() *app.BaseOptions { return &o.BaseOptions }
func (o *Options) Defaults() { o.BaseOptions.Defaults() }
```

Create `internal/routeragent/routeragentserver.go`:

```go
package routeragent

import "project/internal/core/app"

type RouterAgentServer struct{ *app.App }

func Main(opt *Options) error {
	s := NewRouterAgentServer(opt)
	if err := s.Init(); err != nil {
		return err
	}
	return s.Run()
}

func NewRouterAgentServer(opt *Options) *RouterAgentServer {
	s := &RouterAgentServer{App: app.New(&opt.BaseOptions)}
	s.Register(NewModule())
	return s
}
```

- [ ] **Step 6: Run package tests**

Run:

```bash
go test ./internal/lobbysvr ./internal/roomsvr ./internal/matchsvr ./internal/onlinesvr ./internal/routeragent
```

Expected: PASS or no test files, but compilation must succeed.

- [ ] **Step 7: Commit**

```bash
git add internal/lobbysvr internal/roomsvr internal/matchsvr internal/onlinesvr internal/routeragent
git commit -m "feat: add server entrypoints for backend services"
```

---

### Task 4: Add GateServer Entry Point And Options

**Files:**
- Create: `internal/gatesvr/options.go`
- Create: `internal/gatesvr/gateserver.go`
- Modify: `internal/gatesvr/module.go` if needed for constructor/listen-addr naming after lifecycle rename

**Interfaces:**
- Produces: `gatesvr.Options` with `ListenAddr string`
- Produces: `gatesvr.NewOptions() *Options`
- Produces: `gatesvr.Main(opt *Options) error`
- Produces: `gatesvr.NewGateServer(opt *Options) *GateServer`

- [ ] **Step 1: Add gatesvr Options**

Create `internal/gatesvr/options.go`:

```go
package gatesvr

import (
	"time"

	"project/internal/core/app"
)

type Options struct {
	app.BaseOptions
	ListenAddr string
}

func NewOptions() *Options {
	opt := &Options{}
	opt.Defaults()
	return opt
}

func (o *Options) Base() *app.BaseOptions { return &o.BaseOptions }
func (o *Options) Defaults() {
	o.BaseOptions.Defaults()
	o.Tick = 100 * time.Millisecond
	o.ListenAddr = "0.0.0.0:7001"
}
```

- [ ] **Step 2: Add GateServer**

Create `internal/gatesvr/gateserver.go`:

```go
package gatesvr

import "project/internal/core/app"

type GateServer struct{ *app.App }

func Main(opt *Options) error {
	s := NewGateServer(opt)
	if err := s.Init(); err != nil {
		return err
	}
	return s.Run()
}

func NewGateServer(opt *Options) *GateServer {
	s := &GateServer{App: app.New(&opt.BaseOptions)}
	s.Register(NewModule(opt.ListenAddr))
	return s
}
```

- [ ] **Step 3: Confirm gatesvr module lifecycle names**

Ensure `internal/gatesvr/module.go` has these method names after Task 2 rename:

```go
func (m *Module) AfterInit() error { ... }
func (m *Module) BeforeStop() { ... }
func (m *Module) Fini() {}
```

Do not move acceptor/dispatcher internals in this task; keep gate business behavior unchanged.

- [ ] **Step 4: Run package tests**

Run:

```bash
go test ./internal/gatesvr
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gatesvr
git commit -m "feat: add gatesvr server entrypoint"
```

---

### Task 5: Migrate cmd Main Entrypoints To Generic Command

**Files:**
- Modify: `cmd/gatesvr/main.go`
- Modify: `cmd/lobbysvr/main.go`
- Modify: `cmd/roomsvr/main.go`
- Modify: `cmd/matchsvr/main.go`
- Modify: `cmd/onlinesvr/main.go`
- Modify: `cmd/routeragent/main.go`

**Interfaces:**
- Consumes: `corecmd.Command[T]` from Task 1.
- Consumes: each service `Options`, `NewOptions`, and `Main` from Tasks 3 and 4.
- Produces: no `bootstrap` imports and no `app.WithTick` calls in `cmd/*/main.go`.

- [ ] **Step 1: Replace lobbysvr main**

Replace `cmd/lobbysvr/main.go` with:

```go
package main

import (
	"os"

	"project/internal/core/app"
	corecmd "project/internal/core/cmd"
	"project/internal/lobbysvr"
)

func main() {
	cmd := corecmd.Command[*lobbysvr.Options](
		app.CommandMeta{
			Use:   "lobbysvr",
			Short: "大厅服务",
			Confs: []string{"run/lobbysvr/conf/lobbysvr.yaml", "run/lobbysvr/conf/lobbysvr_log.yaml"},
		},
		lobbysvr.NewOptions,
		nil,
		lobbysvr.Main,
	)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Replace roomsvr main**

Replace `cmd/roomsvr/main.go` with:

```go
package main

import (
	"os"

	"project/internal/core/app"
	corecmd "project/internal/core/cmd"
	"project/internal/roomsvr"
)

func main() {
	cmd := corecmd.Command[*roomsvr.Options](
		app.CommandMeta{
			Use:   "roomsvr",
			Short: "对局房间服务",
			Confs: []string{"run/roomsvr/conf/roomsvr.yaml", "run/roomsvr/conf/roomsvr_log.yaml"},
		},
		roomsvr.NewOptions,
		nil,
		roomsvr.Main,
	)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 3: Replace matchsvr main**

Replace `cmd/matchsvr/main.go` with:

```go
package main

import (
	"os"

	"project/internal/core/app"
	corecmd "project/internal/core/cmd"
	"project/internal/matchsvr"
)

func main() {
	cmd := corecmd.Command[*matchsvr.Options](
		app.CommandMeta{
			Use:   "matchsvr",
			Short: "匹配服务",
			Confs: []string{"run/matchsvr/conf/matchsvr.yaml", "run/matchsvr/conf/matchsvr_log.yaml"},
		},
		matchsvr.NewOptions,
		nil,
		matchsvr.Main,
	)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Replace onlinesvr main**

Replace `cmd/onlinesvr/main.go` with:

```go
package main

import (
	"os"

	"project/internal/core/app"
	corecmd "project/internal/core/cmd"
	"project/internal/onlinesvr"
)

func main() {
	cmd := corecmd.Command[*onlinesvr.Options](
		app.CommandMeta{
			Use:   "onlinesvr",
			Short: "在线状态服务",
			Confs: []string{"run/onlinesvr/conf/onlinesvr.yaml", "run/onlinesvr/conf/onlinesvr_log.yaml"},
		},
		onlinesvr.NewOptions,
		nil,
		onlinesvr.Main,
	)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Replace routeragent main**

Replace `cmd/routeragent/main.go` with:

```go
package main

import (
	"os"

	"project/internal/core/app"
	corecmd "project/internal/core/cmd"
	"project/internal/routeragent"
)

func main() {
	cmd := corecmd.Command[*routeragent.Options](
		app.CommandMeta{
			Use:   "routeragent",
			Short: "路由代理（Sidecar）",
			Confs: []string{"run/routeragent/conf/routeragent.yaml", "run/routeragent/conf/routeragent_log.yaml"},
		},
		routeragent.NewOptions,
		nil,
		routeragent.Main,
	)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 6: Replace gatesvr main**

Replace `cmd/gatesvr/main.go` with:

```go
package main

import (
	"os"

	"github.com/spf13/cobra"

	"project/internal/core/app"
	corecmd "project/internal/core/cmd"
	"project/internal/gatesvr"
)

func main() {
	cmd := corecmd.Command[*gatesvr.Options](
		app.CommandMeta{
			Use:   "gatesvr",
			Short: "游戏网关服务",
			Confs: []string{"run/common/conf/", "run/gatesvr/conf/"},
		},
		gatesvr.NewOptions,
		func(cmd *cobra.Command, opt *gatesvr.Options) {
			cmd.Flags().StringVar(&opt.ListenAddr, "listen-addr", opt.ListenAddr, "监听地址")
		},
		gatesvr.Main,
	)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 7: Build all commands**

Run:

```bash
go test ./cmd/gatesvr ./cmd/lobbysvr ./cmd/roomsvr ./cmd/matchsvr ./cmd/onlinesvr ./cmd/routeragent
```

Expected: PASS or no test files, but all command packages compile.

- [ ] **Step 8: Commit**

```bash
git add cmd/gatesvr cmd/lobbysvr cmd/roomsvr cmd/matchsvr cmd/onlinesvr cmd/routeragent
git commit -m "refactor: use generic command entrypoints"
```

---

### Task 6: Remove Bootstrap Package And Legacy Callers

**Files:**
- Delete: `internal/core/bootstrap/bootstrap.go`
- Delete: `internal/core/bootstrap/cobra.go`
- Confirm no imports remain: all `.go` files
- Confirm no `app.WithTick`, `WithReadyTimeout`, `WithDrainTimeout` remain

**Interfaces:**
- Consumes: `app.ValidateConfig`, `corecmd.AddVersionCmd`, `config.OverrideServerIndex`.
- Produces: zero `internal/core/bootstrap` imports.

- [ ] **Step 1: Verify old symbols are unused**

Run:

```bash
rg "internal/core/bootstrap|bootstrap\.|app\.With|WithTick|WithReadyTimeout|WithDrainTimeout" -g'*.go'
```

Expected: no output. If output exists, migrate that file to the new APIs before deletion.

- [ ] **Step 2: Delete bootstrap files**

Delete:

```text
internal/core/bootstrap/bootstrap.go
internal/core/bootstrap/cobra.go
```

Delete:

```text
internal/core/app/option.go
```

- [ ] **Step 3: Run package list check**

Run:

```bash
go list ./... >/tmp/game-server-pro-packages.txt
```

Expected: command exits 0. If it fails because of existing unrelated untracked `tools/e2e_full` work, capture the exact error and only fix references caused by this refactor.

- [ ] **Step 4: Commit**

```bash
git add -A internal/core/bootstrap internal/core/app/option.go
git commit -m "refactor: remove bootstrap compatibility layer"
```

---

### Task 7: Update Tests And Run Full Validation

**Files:**
- Modify any remaining tests with old lifecycle names.
- Modify any docs/build references to bootstrap ldflags if present.

**Interfaces:**
- Consumes: all previous tasks.
- Produces: full repository builds with new startup architecture.

- [ ] **Step 1: Search for forbidden old symbols**

Run:

```bash
rg "OnAfterInit|OnBeforeStop|OnStop|internal/core/bootstrap|bootstrap\.|app\.With|type Option func|func WithTick|func WithReadyTimeout|func WithDrainTimeout" -g'*.go'
```

Expected: no output.

- [ ] **Step 2: Search for old version ldflags path**

Run:

```bash
rg "internal/core/bootstrap\.Version|internal/core/bootstrap\.BuildTime|bootstrap.Version|bootstrap.BuildTime" -g'*'
```

Expected: either no output or build scripts/docs that must be updated to:

```text
project/internal/core/cmd.Version
project/internal/core/cmd.BuildTime
```

- [ ] **Step 3: Update ldflags references if found**

If Step 2 finds files such as `Makefile` or docs, replace old paths with:

```text
-X project/internal/core/cmd.Version=$(git describe --tags --always)
-X project/internal/core/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)
```

- [ ] **Step 4: Run focused core tests**

Run:

```bash
go test ./internal/core/app ./internal/core/cmd ./internal/core/config ./internal/core/log ./internal/core/ragent
```

Expected: PASS.

- [ ] **Step 5: Run service compile tests**

Run:

```bash
go test ./internal/gatesvr ./internal/lobbysvr ./internal/roomsvr ./internal/matchsvr ./internal/onlinesvr ./internal/routeragent ./cmd/...
```

Expected: PASS or no test files.

- [ ] **Step 6: Run broader test suite**

Run:

```bash
go test ./...
```

Expected: PASS. If existing unrelated `tools/e2e_full` tests fail due environment prerequisites, record the exact failing packages and rerun all non-e2e packages:

```bash
go test $(go list ./... | grep -v '/tools/e2e_full')
```

- [ ] **Step 7: Commit validation fixes**

```bash
git add -A
git commit -m "test: update startup lifecycle references"
```

---

## Self-Review

**Spec coverage:**
- Module → Server: Tasks 3 and 4 create Server wrappers and `Main(opt)` for every service.
- bootstrap融入 app: Tasks 1, 2, and 6 move validation/options/version/new-app behavior out of bootstrap and delete the package.
- Init → Run → Fini: Task 2 implements the new App lifecycle and module hook names.
- cobra isolation: Task 1 creates `internal/core/cmd`; Task 5 moves all command mains to `corecmd.Command[T]`.
- Every service owns Options/NewOptions/Main: Tasks 3 and 4.
- No reflection: Task 1 `Command` receives `newOptions func() T`.
- Existing tests updated: Tasks 2 and 7.

**Placeholder scan:** No TBD/TODO/fill-later steps are present. Every code-producing step includes exact code snippets or exact symbol mappings.

**Type consistency:**
- `app.Options` requires `Base() *BaseOptions` and `Defaults()`.
- `corecmd.Command[T app.Options]` accepts `newOptions func() T`, `addFlags func(*cobra.Command, T)`, `run func(T) error`.
- Service `Main` signatures match the generic command `run` parameter: `func Main(opt *Options) error`.
- Service constructors use `app.New(&opt.BaseOptions)` consistently.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-06-21-server-bootstrap-implementation.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
