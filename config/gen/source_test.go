package configgen

import "testing"

func TestCheckGateReloadAllowsReloadFields(t *testing.T) {
	current := &GateConfig{
		Gate: GateRuntimeConfig{
			ListenTcp:    "127.0.0.1:7001",
			ListenWs:     "127.0.0.1:7002",
			MaxConn:      100,
			HeartbeatSec: 30,
		},
		Log: LogConfig{
			Level:        "info",
			Format:       "console",
			StderrAlso:   true,
			Dir:          "./logs",
			Basename:     "gate",
			MaxSizeMb:    100,
			MaxBackups:   10,
			RotateByHour: true,
		},
	}
	next := *current
	next.Gate.HeartbeatSec = 60

	if err := CheckGateReload(&next, current); err != nil {
		t.Fatalf("CheckGateReload() error = %v", err)
	}
}

func TestCheckGateReloadRejectsImmutableFields(t *testing.T) {
	current := &GateConfig{
		Gate: GateRuntimeConfig{
			ListenTcp:    "127.0.0.1:7001",
			ListenWs:     "127.0.0.1:7002",
			MaxConn:      100,
			HeartbeatSec: 30,
		},
		Log: LogConfig{
			Level:        "info",
			Format:       "console",
			StderrAlso:   true,
			Dir:          "./logs",
			Basename:     "gate",
			MaxSizeMb:    100,
			MaxBackups:   10,
			RotateByHour: true,
		},
	}
	next := *current
	next.Gate.MaxConn = 200

	if err := CheckGateReload(&next, current); err == nil {
		t.Fatal("CheckGateReload() error = nil, want immutable field error")
	}
}
