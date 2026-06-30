package logger_test

import (
	"errors"
	"os"
	"time"

	"project/src/common/logger"
)

// Example_init 演示如何从配置初始化 logger（main 里调用一次）
func Example_init() {
	// 典型做法：从配置文件反序列化 FileLoggerConfig，这里用字面量代替
	cfg := logger.FileLoggerConfig{
		Level:      logger.InfoLevel,
		Format:     logger.FormatConsole, // 开发用 console，生产用 json
		StderrAlso: true,                 // 开发期同时打到终端
		Rotate: logger.RotateConfig{
			Dir:          "./logs",
			Basename:     "scenesvr",
			MaxSizeMB:    100,
			MaxBackups:   72,
			RotateByHour: true,
		},
	}

	log, closer, err := logger.NewZapFileLogger(cfg)
	if err != nil {
		// 日志初始化失败直接退出，无法降级
		os.Exit(1)
	}
	defer closer.Close() // flush + 关文件，程序退出前执行

	logger.SetGlobal(log)
}

// Example_basic 演示最常用的日志写法
func Example_basic() {
	// 包级快捷函数，等价于 logger.G().Info(...)
	logger.Info("server started", logger.String("addr", ":8080"))
	logger.Warn("high latency", logger.Int64("ms", 320), logger.String("route", "/login"))
	logger.Error("db error", logger.Err(errors.New("connection refused")))
	logger.Debug("detail", logger.Int("count", 10)) // level < Info 时不输出
}

// Example_with 演示 With 派生子 logger，绑定固定字段
// 适合在 room / session 等有生命周期的对象里使用
func Example_with() {
	// 创建绑定了 room_id 的子 logger
	roomLog := logger.With(logger.Int64("room_id", 1001))

	// 后续所有日志自动带上 room_id
	roomLog.Info("round started")
	roomLog.Info("player joined", logger.String("uid", "u_abc"))
	roomLog.Warn("player disconnected", logger.String("uid", "u_abc"))

	// 可以继续派生，叠加字段
	playerLog := roomLog.With(logger.String("uid", "u_abc"))
	playerLog.Info("action received", logger.String("action", "move"))
}

// Example_hotPath 演示热路径下避免无效开销
func Example_hotPath() {
	// Debug 在生产关闭时，IsEnabled 返回 false，直接跳过
	// 避免先构造了昂贵的 fields 再发现 level 不够
	if logger.G().IsEnabled(logger.DebugLevel) {
		logger.Debug("frame snapshot",
			logger.Any("state", map[string]int{"hp": 100, "mp": 80}),
		)
	}
}

// Example_fields 演示所有支持的强类型字段
func Example_fields() {
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
		logger.Any("extra", struct{ X int }{X: 1}), // 兜底，有反射开销
	)
}

// Example_sugared 演示 SugaredLogger，用于低频路径（启动、关闭、错误处理）
func Example_sugared() {
	s := logger.Sugar()

	// f 系列：printf 风格，适合拼接多个变量的描述性日志
	s.Infof("player %s joined room %d", "u_abc", 1001)
	s.Errorf("failed to save: %v", errors.New("disk full"))
	s.Infof("round ended, room_id=%d duration_ms=%d", 1001, 30000)

	// 包级快捷函数，等价于 logger.Sugar().Infof(...)
	logger.Infof("scenesvr start failed retcode=%d", 100)
}
