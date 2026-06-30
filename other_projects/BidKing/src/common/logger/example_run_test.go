package logger_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"project/src/common/logger"
)

func initTestLogger(t *testing.T) func() {
	t.Helper()
	cfg := logger.FileLoggerConfig{
		Level:      logger.DebugLevel,
		Format:     logger.FormatConsole,
		StderrAlso: true,
		Rotate: logger.RotateConfig{
			Dir:          t.TempDir(),
			Basename:     "test",
			MaxSizeMB:    10,
			MaxBackups:   5,
			RotateByHour: true,
		},
	}
	log, closer, err := logger.NewZapFileLogger(cfg)
	if err != nil {
		t.Fatal(err)
	}
	logger.SetGlobal(log)
	return func() { closer.Close() }
}

func TestExample_basic(t *testing.T) {
	defer initTestLogger(t)()

	logger.Info("server started", logger.String("addr", ":8080"))
	logger.Warn("high latency", logger.Int64("ms", 320), logger.String("route", "/login"))
	logger.Error("db error", logger.Err(errors.New("connection refused")))
	logger.Debug("detail", logger.Int("count", 10))
}

func TestExample_with(t *testing.T) {
	defer initTestLogger(t)()

	roomLog := logger.With(logger.Int64("room_id", 1001))
	roomLog.Info("round started")
	roomLog.Info("player joined", logger.String("uid", "u_abc"))
	roomLog.Warn("player disconnected", logger.String("uid", "u_abc"))

	playerLog := roomLog.With(logger.String("uid", "u_abc"))
	playerLog.Info("action received", logger.String("action", "move"))
}

func TestExample_hotPath(t *testing.T) {
	defer initTestLogger(t)()

	if logger.G().IsEnabled(logger.DebugLevel) {
		logger.Debug("frame snapshot",
			logger.Any("state", map[string]int{"hp": 100, "mp": 80}),
		)
	}
}

func TestExample_fields(t *testing.T) {
	defer initTestLogger(t)()

	logger.Info("all field types",
		logger.String("s", "hello"),
		logger.Int("i", 42),
		logger.Int32("i32", 100),
		logger.Int64("i64", 9999),
		logger.Uint32("u32", 200),
		logger.Uint64("u64", 300),
		logger.Float64("f64", 3.14),
		logger.Bool("ok", true),
		logger.Duration("elapsed", 120*time.Millisecond),
		logger.Time("ts", time.Now()),
		logger.Err(errors.New("something failed")),
		logger.NamedErr("cause", errors.New("timeout")),
		logger.Any("extra", struct{ X int }{X: 1}),
	)
}

func TestExample_sugared(t *testing.T) {
	defer initTestLogger(t)()

	s := logger.Sugar()
	s.Infof("player %s joined room %d", "u_abc", 1001)
	s.Errorf("failed to save: %v", errors.New("disk full"))
	s.Infof("round ended, room_id=%d duration_ms=%d", 1001, 30000)

	logger.Infof("scenesvr start failed retcode=%d", 100)
}

func TestExample_initFromConfig(t *testing.T) {
	cfg := logger.FileLoggerConfig{
		Level:      logger.InfoLevel,
		Format:     logger.FormatJSON,
		StderrAlso: true,
		Rotate: logger.RotateConfig{
			Dir:          t.TempDir(),
			Basename:     "scenesvr",
			MaxSizeMB:    100,
			MaxBackups:   72,
			RotateByHour: true,
		},
	}
	log, closer, err := logger.NewZapFileLogger(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	logger.SetGlobal(log)

	logger.Info("server started", logger.String("addr", ":8080"))
	logger.Warn("high latency", logger.Int64("ms", 320))

	// 验证日志文件已生成
	entries, err := os.ReadDir(cfg.Rotate.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Error("expected log files to be created")
	}
	for _, e := range entries {
		t.Logf("log file: %s", e.Name())
	}
}
