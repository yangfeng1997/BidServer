package log

import (
	"os"
	"path/filepath"
	"testing"

	"project/conf/schema/gen"
)

func TestLoggerModule_ThreeInstances(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	lg := &gen.LogGroupConfig{
		Main:    &gen.LogConfig{Level: "info", Format: "console", StderrAlso: false, Dir: logDir, Basename: "gatesvr", MaxSizeMb: 10, MaxBackups: 2, RotateByHour: false},
		Res:     &gen.LogConfig{Level: "debug", Format: "console", StderrAlso: false, Dir: logDir, Basename: "gatesvr_res", MaxSizeMb: 10, MaxBackups: 2, RotateByHour: false},
		Tracing: &gen.LogConfig{Level: "debug", Format: "json", StderrAlso: false, Dir: logDir, Basename: "gatesvr_trace", MaxSizeMb: 10, MaxBackups: 2, RotateByHour: false},
	}
	SetLogGroupGetter(func() *gen.LogGroupConfig { return lg })
	defer func() { logGroupGetter = nil }()

	logMod := NewModule()
	if err := logMod.AfterInit(); err != nil {
		t.Fatalf("log: %v", err)
	}
	defer func() {
		logMod.BeforeStop()
		logMod.Fini()
	}()

	if Main == nil || Res == nil || Tracing == nil {
		t.Fatal("one or more log instances are nil")
	}
	if Main == Res || Main == Tracing || Res == Tracing {
		t.Error("three log instances should be independent")
	}

	Main.Info("main log entry")
	Res.Info("res log entry")
	Tracing.Info("tracing log entry")

	logMod.Fini()

	for _, name := range []string{"gatesvr.log", "gatesvr_res.log", "gatesvr_trace.log"} {
		path := filepath.Join(logDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
		t.Logf("%s: %d bytes", name, info.Size())
	}
}
