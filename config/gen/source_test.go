package configgen

import "testing"

func TestCheckGateReloadAllowsReloadFields(t *testing.T) {
	current := newTestGateConfig()
	candidate := *current
	candidate.HeartbeatSec = 60

	if err := CheckGateReload(&candidate, current); err != nil {
		t.Fatalf("CheckGateReload() error = %v", err)
	}
}

func TestCheckGateReloadRejectsImmutableFields(t *testing.T) {
	current := newTestGateConfig()
	candidate := *current
	candidate.MaxConn = 200

	if err := CheckGateReload(&candidate, current); err == nil {
		t.Fatal("CheckGateReload() error = nil, want immutable field error")
	}
}

func newTestGateConfig() *GateConfig {
	return &GateConfig{
		ListenTcp:    "127.0.0.1:7001",
		ListenWs:     "127.0.0.1:7002",
		MaxConn:      100,
		HeartbeatSec: 30,
		LogGroup: LogGroupConfig{
			Main:    newTestLogConfig("gate"),
			Res:     newTestLogConfig("gate_res"),
			Tracing: newTestLogConfig("gate_tracing"),
		},
	}
}

func newTestLogConfig(basename string) LogConfig {
	return LogConfig{
		Level:        "info",
		Format:       "console",
		StderrAlso:   true,
		Dir:          "./logs",
		Basename:     basename,
		MaxSizeMb:    100,
		MaxBackups:   10,
		RotateByHour: true,
	}
}
