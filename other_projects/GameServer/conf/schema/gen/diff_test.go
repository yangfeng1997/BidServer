package gen

import "testing"

func TestCheckStatic_GateConfig(t *testing.T) {
	old := &GateConfig{ListenTcp: "0.0.0.0:7001", ListenWs: "0.0.0.0:7002", DrainTimeoutSec: 5, MaxConn: 10000, LogLevel: "info", HeartbeatSec: 30}

	// reload-only 字段变更（max_conn/log_level/heartbeat_sec）：应允许
	reloadOnly := &GateConfig{ListenTcp: "0.0.0.0:7001", ListenWs: "0.0.0.0:7002", DrainTimeoutSec: 5, MaxConn: 20000, LogLevel: "debug", HeartbeatSec: 15}
	if stale := reloadOnly.CheckStatic(old); len(stale) != 0 {
		t.Fatalf("reload-only changes must be allowed, got %v", stale)
	}

	// 静态字段变更（listen_tcp）：应拒绝
	staticChg := &GateConfig{ListenTcp: "0.0.0.0:9999", ListenWs: "0.0.0.0:7002", DrainTimeoutSec: 5, MaxConn: 10000, LogLevel: "info", HeartbeatSec: 30}
	if stale := staticChg.CheckStatic(old); len(stale) == 0 || stale[0] != "listen_tcp" {
		t.Fatalf("static change must be reported, got %v", stale)
	}
}

func TestCheckStatic_LobbyConfig(t *testing.T) {
	// LobbyConfig 所有字段都是 reload → CheckStatic 应永远返回空
	old := &LobbyConfig{LogLevel: "info", HeartbeatSec: 30, MaxPlayer: 100}
	chg := &LobbyConfig{LogLevel: "debug", HeartbeatSec: 15, MaxPlayer: 200}
	if stale := chg.CheckStatic(old); len(stale) != 0 {
		t.Fatalf("all-reload config must allow changes, got %v", stale)
	}
}

func TestCheckStatic_GatesvrConfig_nested(t *testing.T) {
	old := &GatesvrConfig{Gate: &GateConfig{ListenTcp: "a", ListenWs: "b"}, Log: &LogGroupConfig{}}
	chg := &GatesvrConfig{Gate: &GateConfig{ListenTcp: "c", ListenWs: "b"}, Log: &LogGroupConfig{}}
	stale := chg.CheckStatic(old)
	if len(stale) == 0 || stale[0] != "gate.listen_tcp" {
		t.Fatalf("nested static change must be prefixed, got %v", stale)
	}
}

func TestCheckStatic_nil(t *testing.T) {
	var c *GateConfig
	if stale := c.CheckStatic(&GateConfig{}); len(stale) == 0 {
		t.Fatal("expected <nil> error for nil receiver")
	}
}
