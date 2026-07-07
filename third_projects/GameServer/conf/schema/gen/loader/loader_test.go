package config

import (
	"os"
	"path/filepath"
	"testing"

	config "project/internal/core/config"
)

func TestRegisterGatesvr(t *testing.T) {
	config.ResetForTest()
	dir := t.TempDir()
	svcDir := filepath.Join(dir, "run", "gatesvr", "conf")
	os.MkdirAll(svcDir, 0o755)
	os.WriteFile(filepath.Join(svcDir, "gatesvr.yaml"), []byte("gate:\n  listen_tcp: \"0.0.0.0:7001\"\n  listen_ws: \"0.0.0.0:7002\"\n  drain_timeout_sec: 5\n  max_conn: 10000\n  log_level: info\n  heartbeat_sec: 30\n"), 0o644)
	os.WriteFile(filepath.Join(svcDir, "gatesvr_log.yaml"), []byte("log:\n  main:\n    level: info\n    format: console\n    dir: /tmp/log\n    basename: gatesvr\n"), 0o644)

	RegisterGatesvr([]string{
		filepath.Join(svcDir, "gatesvr.yaml"),
		filepath.Join(svcDir, "gatesvr_log.yaml"),
	})

	// 注册后单例非 nil，通过 config.Loader 接口调用三段式
	if gatesvr == nil {
		t.Fatal("gatesvr is nil after RegisterGatesvr")
	}
	if err := gatesvr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if errs := gatesvr.Validate(); len(errs) > 0 {
		t.Fatalf("Validate: %v", errs)
	}
	gatesvr.Swap()

	cfg := GatesvrConfig()
	if cfg == nil {
		t.Fatal("GatesvrConfig returned nil")
	}
	if cfg.Gate.MaxConn != 10000 {
		t.Fatalf("MaxConn = %d, want 10000", cfg.Gate.MaxConn)
	}
}

func TestGatesvrConfig_CheckStatic(t *testing.T) {
	config.ResetForTest()
	dir := t.TempDir()
	svcDir := filepath.Join(dir, "run", "gatesvr", "conf")
	os.MkdirAll(svcDir, 0o755)
	os.WriteFile(filepath.Join(svcDir, "svc.yaml"), []byte("gate:\n  listen_tcp: \"0.0.0.0:7001\"\n  listen_ws: \"0.0.0.0:7002\"\n  drain_timeout_sec: 5\n  max_conn: 10000\n  log_level: info\n  heartbeat_sec: 30\n"), 0o644)

	RegisterGatesvr([]string{filepath.Join(svcDir, "svc.yaml")})
	gatesvr.Load()
	gatesvr.Swap()

	// 改 reload 字段：应允许
	os.WriteFile(filepath.Join(svcDir, "svc.yaml"), []byte("gate:\n  listen_tcp: \"0.0.0.0:7001\"\n  listen_ws: \"0.0.0.0:7002\"\n  drain_timeout_sec: 5\n  max_conn: 20000\n  log_level: debug\n  heartbeat_sec: 15\n"), 0o644)
	_ = gatesvr.Load()
	if stale := gatesvr.Check(); len(stale) != 0 {
		t.Fatalf("reload-only changes must be allowed, got stale: %v", stale)
	}

	// 改静态字段：应拒绝
	os.WriteFile(filepath.Join(svcDir, "svc.yaml"), []byte("gate:\n  listen_tcp: \"0.0.0.0:9999\"\n  listen_ws: \"0.0.0.0:7002\"\n  drain_timeout_sec: 5\n  max_conn: 10000\n  log_level: info\n  heartbeat_sec: 30\n"), 0o644)
	_ = gatesvr.Load()
	stale := gatesvr.Check()
	if len(stale) == 0 {
		t.Fatal("static change must be rejected, got empty stale")
	}
	if stale[0] != "gate.listen_tcp" {
		t.Fatalf("stale[0] = %q, want gate.listen_tcp", stale[0])
	}
}
