package config

import (
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"project/conf/schema/gen"
	"project/internal/core/app"
	"project/pkg/configgen"
)

// ── mockLoader 实现 config.Loader，让测试可控返回 Check/Validate 结果 ──

type mockLoader struct {
	loadErr     error
	checkResult []string
	valResult   []string
	swapCalled  bool
	loaded      bool
}

func (m *mockLoader) Load() error       { m.loaded = true; return m.loadErr }
func (m *mockLoader) Check() []string   { return m.checkResult }
func (m *mockLoader) Validate() []string { return m.valResult }
func (m *mockLoader) Swap()             { m.swapCalled = true }
func (m *mockLoader) LogGroup() *gen.LogGroupConfig { return nil }

func resetGlobals() {
	serviceLoader = nil
	signal.Reset(syscall.SIGHUP)
}

// ── 基础生命周期 ──

func TestModule_LoadService(t *testing.T) {
	resetGlobals()
	mock := &mockLoader{}
	RegisterService(mock)

	m := NewModule()
	m.Init(&app.App{})
	if err := m.AfterInit(); err != nil {
		t.Fatalf("AfterInit: %v", err)
	}
	if !mock.swapCalled {
		t.Fatal("Swap was not called")
	}
	m.BeforeStop()
}

// ── 服务 Loader 未注册 ──

func TestModule_NoServiceLoader(t *testing.T) {
	resetGlobals()
	m := NewModule()
	m.Init(&app.App{})
	if err := m.AfterInit(); err == nil {
		t.Fatal("expected error for missing service loader")
	}
}

// ── 热更：可热更字段 ──

func TestModule_HotReload_OK(t *testing.T) {
	resetGlobals()
	mock := &mockLoader{}
	RegisterService(mock)

	m := NewModule()
	m.Init(&app.App{})
	_ = m.AfterInit()

	mock.swapCalled = false
	if err := m.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !mock.swapCalled {
		t.Fatal("Swap not called on successful reload")
	}
}

// ── 热更：静态字段拒绝 ──

func TestModule_HotReload_StaticRejected(t *testing.T) {
	resetGlobals()
	mock := &mockLoader{checkResult: []string{"gate.listen_tcp"}}
	RegisterService(mock)

	m := NewModule()
	m.Init(&app.App{})
	_ = m.AfterInit()

	if err := m.Reload(); err == nil {
		t.Fatal("expected reload to be rejected due to static field change")
	}
}

// ── 信号分发端到端：SIGHUP → Poster.Post → Reload() ──

type trackPoster struct {
	posted chan struct{}
}

func (p *trackPoster) Post(fn func()) {
	go func() {
		fn()
		p.posted <- struct{}{}
	}()
}

func TestModule_SignalDispatch(t *testing.T) {
	resetGlobals()
	mock := &mockLoader{}
	RegisterService(mock)

	m := NewModule()
	m.Init(&app.App{})

	poster := &trackPoster{posted: make(chan struct{}, 1)}
	m.poster = poster

	if err := m.AfterInit(); err != nil {
		t.Fatalf("AfterInit: %v", err)
	}
	defer func() {
		m.BeforeStop()
		signal.Reset(syscall.SIGHUP)
	}()

	time.Sleep(10 * time.Millisecond)

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatalf("SIGHUP: %v", err)
	}

	select {
	case <-poster.posted:
		t.Log("SIGHUP → Post → Reload dispatched successfully")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for SIGHUP → Post dispatch")
	}

	if !mock.loaded {
		t.Fatal("reload was not triggered")
	}
}

// ── SIGHUP 端到端：整条链路（文件修改 → 信号 → 新值可读）──

func TestModule_SIGHUP_E2E(t *testing.T) {
	resetGlobals()
	dir := t.TempDir()
	svcDir := filepath.Join(dir, "run", "gatesvr", "conf")
	os.MkdirAll(svcDir, 0o755)
	svcFile := filepath.Join(svcDir, "svc.yaml")
	os.WriteFile(svcFile, []byte("gate:\n  listen_tcp: \"0.0.0.0:7001\"\n  listen_ws: \"0.0.0.0:7002\"\n  drain_timeout_sec: 5\n  max_conn: 10000\n  log_level: info\n  heartbeat_sec: 30\n"), 0o644)

	l := &realLoader{}
	l.loadFn = func() error {
		cfg, err := configgen.LoadFiles[*gen.GatesvrConfig](svcFile)
		if err != nil {
			return err
		}
		l.shadowPtr = cfg
		return nil
	}
	l.shadowPtr = &gen.GatesvrConfig{}
	RegisterService(l)

	m := NewModule()
	m.Init(&app.App{})

	poster := &trackPoster{posted: make(chan struct{}, 1)}
	m.poster = poster

	if err := m.AfterInit(); err != nil {
		t.Fatalf("AfterInit: %v", err)
	}
	defer func() {
		m.BeforeStop()
		signal.Reset(syscall.SIGHUP)
	}()

	cfg := l.Get()
	if cfg == nil || cfg.Gate.MaxConn != 10000 {
		t.Fatalf("initial max_conn = %d, want 10000", cfg.Gate.MaxConn)
	}

	os.WriteFile(svcFile, []byte("gate:\n  listen_tcp: \"0.0.0.0:7001\"\n  listen_ws: \"0.0.0.0:7002\"\n  drain_timeout_sec: 5\n  max_conn: 99999\n  log_level: debug\n  heartbeat_sec: 60\n"), 0o644)

	l.loadFn = func() error {
		cfg, err := configgen.LoadFiles[*gen.GatesvrConfig](svcFile)
		if err != nil {
			return err
		}
		l.shadowPtr = cfg
		return nil
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatalf("SIGHUP: %v", err)
	}

	select {
	case <-poster.posted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for reload")
	}

	cfg = l.Get()
	if cfg == nil || cfg.Gate.MaxConn != 99999 {
		t.Fatalf("post-reload max_conn = %d, want 99999", cfg.Gate.MaxConn)
	}
	if cfg.Gate.LogLevel != "debug" {
		t.Fatalf("post-reload log_level = %s, want debug", cfg.Gate.LogLevel)
	}
}

// ── realLoader 实现 config.Loader ──

type realLoader struct {
	loadFn    func() error
	shadowPtr *gen.GatesvrConfig
	cur       atomic.Value
}

func (l *realLoader) Load() error {
	if l.loadFn != nil { return l.loadFn() }
	return nil
}
func (l *realLoader) Check() []string {
	cur := l.cur.Load()
	if cur == nil { return nil }
	return l.shadowPtr.CheckStatic(cur.(*gen.GatesvrConfig))
}
func (l *realLoader) Validate() []string {
	if l.shadowPtr == nil { return nil }
	return l.shadowPtr.Validate()
}
func (l *realLoader) Swap() { l.cur.Store(l.shadowPtr) }
func (l *realLoader) Get() *gen.GatesvrConfig {
	v := l.cur.Load()
	if v == nil { return nil }
	return v.(*gen.GatesvrConfig)
}
