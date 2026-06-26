package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"
)

// // Backend 适配到 uber-go/zap
type ZapBackend struct {
	z         *zap.Logger
	atomLevel zap.AtomicLevel
}

// NewZapBackend 用已有的 *zap.Logger 创建 ZapBackend
func NewZapBackend(z *zap.Logger) *ZapBackend {
	return &ZapBackend{z: z}
}

// SetLevel 运行时切换日志级别（热更路径，atomic.Store，无锁）
func (b *ZapBackend) SetLevel(l Level) {
	b.atomLevel.SetLevel(toZapLevel(l))
}

// NewZapDevelopment 快速创建开发用 Logger（彩色、debug 级别、调用栈）
func NewZapDevelopment() (Logger, error) {
	z, err := zap.NewDevelopment()
	if err != nil {
		return nil, err
	}
	return New(NewZapBackend(z)), nil
}

// NewZapProduction 快速创建生产用 Logger（JSON、info 级别）
func NewZapProduction() (Logger, error) {
	z, err := zap.NewProduction()
	if err != nil {
		return nil, err
	}
	return New(NewZapBackend(z)), nil
}

// NewZapProductionWithConfig 用自定义 zap.Config 创建 Logger
func NewZapProductionWithConfig(cfg zap.Config) (Logger, error) {
	z, err := cfg.Build()
	if err != nil {
		return nil, err
	}
	return New(NewZapBackend(z)), nil
}

// Sync 刷新 zap 缓冲（程序退出前调用）
func (b *ZapBackend) Sync() error {
	return b.z.Sync()
}

// InternalLogger 返回底层 *zap.Logger，供需要直接操作 zap 的场景使用
func (b *ZapBackend) InternalLogger() *zap.Logger {
	return b.z
}

// ---------- Backend 接口实现 ----------

func (b *ZapBackend) IsEnabled(level Level) bool {
	return b.z.Core().Enabled(toZapLevel(level))
}

func (b *ZapBackend) With(fields []Field) Backend {
	return &ZapBackend{z: b.z.With(toZapFields(fields)...)}
}

func (b *ZapBackend) Log(level Level, msg string, fields []Field) {
	zfs := toZapFields(fields)
	switch level {
	case DebugLevel:
		b.z.Debug(msg, zfs...)
	case InfoLevel:
		b.z.Info(msg, zfs...)
	case WarnLevel:
		b.z.Warn(msg, zfs...)
	case ErrorLevel:
		b.z.Error(msg, zfs...)
	case FatalLevel:
		b.z.Fatal(msg, zfs...)
	}
}

// ---------- 格式化输出 + 滚动日志构造器 ----------

// Format 日志输出格式
type Format string

const (
	FormatConsole Format = "console" // [time][level] [caller] msg fields..
	FormatJSON    Format = "json"    // {"T":...,"L":...,"C":...,"M":...,...}
)

// FileLoggerConfig 文件日志完整配置
type FileLoggerConfig struct {
	Level      Level        `yaml:"level"`
	Format     Format       `yaml:"format"`
	StderrAlso bool         `yaml:"stderr_also"`
	CallerSkip int          `yaml:"caller_skip"`
	Rotate     RotateConfig `yaml:"rotate"`
}

// LogCloser 同时负责 flush zap buffer 和关闭底层文件，程序退出前调用
type LogCloser struct {
	zb *ZapBackend
	rw io.Closer
}

func (c *LogCloser) Close() error {
	_ = c.zb.Sync()
	return c.rw.Close()
}

// SetLevel 运行时切换日志级别
func (c *LogCloser) SetLevel(l Level) { c.zb.SetLevel(l) }

// NewZapFileLogger 创建带格式化输出和滚动的文件 Logger
// Format="console"（默认）：[2006-01-02 15:04:05.000][info] [a/b/c.go:42] msg  fields
// Format="json"：{"T":"...","L":"info","C":"a/b/c.go:42","M":"msg",...}
func NewZapFileLogger(cfg FileLoggerConfig) (Logger, *LogCloser, error) {
	rw, err := NewRotatingWriter(cfg.Rotate)
	if err != nil {
		return nil, nil, err
	}

	var enc zapcore.Encoder
	switch cfg.Format {
	case FormatJSON:
		enc = zapcore.NewJSONEncoder(jsonEncoderConfig())
	default:
		enc = zapcore.NewConsoleEncoder(bracketEncoderConfig())
	}

	var w zapcore.WriteSyncer
	if cfg.StderrAlso {
		w = zapcore.NewMultiWriteSyncer(zapcore.AddSync(rw), zapcore.AddSync(os.Stderr))
	} else {
		w = zapcore.AddSync(rw)
	}

	atom := zap.NewAtomicLevelAt(toZapLevel(cfg.Level))
	core := zapcore.NewCore(enc, w, atom)
	z := zap.New(core,
		zap.AddCaller(),
		zap.AddCallerSkip(2+cfg.CallerSkip),
		zap.WithFatalHook(zapcore.WriteThenPanic),
	)
	zb := &ZapBackend{z: z, atomLevel: atom}
	return New(zb), &LogCloser{zb: zb, rw: rw}, nil
}

// bracketEncoderConfig 返回 [time][level] [caller] 风格的 EncoderConfig
func bracketEncoderConfig() zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
		TimeKey:          "T",
		LevelKey:         "L",
		CallerKey:        "C",
		MessageKey:       "M",
		LineEnding:       zapcore.DefaultLineEnding,
		EncodeTime:       bracketTimeEncoder,
		EncodeLevel:      bracketLevelEncoder,
		EncodeCaller:     bracketCallerEncoder,
		EncodeDuration:   zapcore.StringDurationEncoder,
		ConsoleSeparator: " ",
	}
}

// bracketTimeEncoder 输出 [2006-01-02 15:04:05.000]
func bracketTimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString("[" + t.Format("2006-01-02 15:04:05.000") + "]")
}

// bracketLevelEncoder 输出 [debug] / [info] 等
func bracketLevelEncoder(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString("[" + l.String() + "]")
}

// bracketCallerEncoder 输出 [a/b/c.go:42]，保留 3 层路径
func bracketCallerEncoder(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString("[" + trimCallerPath(caller.File, 3) + ":" + strconv.Itoa(caller.Line) + "]")
}

// jsonEncoderConfig JSON 格式，key 名与 bracketEncoderConfig 一致
func jsonEncoderConfig() zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
		TimeKey:        "T",
		LevelKey:       "L",
		CallerKey:      "C",
		MessageKey:     "M",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeTime:     zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000"),
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeCaller:   jsonCallerEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
	}
}

// jsonCallerEncoder 输出 "a/b/c.go:42"（JSON 值本身有引号，不需要方括号）
func jsonCallerEncoder(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(trimCallerPath(caller.File, 3) + ":" + strconv.Itoa(caller.Line))
}

func trimCallerPath(full string, depth int) string {
	full = filepath.ToSlash(full)
	parts := strings.Split(full, "/")
	if len(parts) <= depth {
		return full
	}
	return strings.Join(parts[len(parts)-depth:], "/")
}

// NewZapLoggerFromFile 从 YAML 文件加载 FileLoggerConfig 并创建文件日志 Logger
// path 为 YAML 文件路径，格式对应 FileLoggerConfig struct 的 yaml tag
func NewZapLoggerFromFile(path string) (Logger, *LogCloser, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read log config %s: %w", path, err)
	}
	var cfg FileLoggerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, nil, fmt.Errorf("parse log config %s: %w", path, err)
	}
	return NewZapFileLogger(cfg)
}

// ---------- 转换辅助 ----------

func toZapLevel(l Level) zapcore.Level {
	switch l {
	case DebugLevel:
		return zapcore.DebugLevel
	case WarnLevel:
		return zapcore.WarnLevel
	case ErrorLevel:
		return zapcore.ErrorLevel
	case FatalLevel:
		return zapcore.FatalLevel
	default:
		return zapcore.InfoLevel
	}
}

// toZapFields 将强类型 []Field 转换为 []zap.Field，无反射分支走零分配路径
func toZapFields(fields []Field) []zap.Field {
	if len(fields) == 0 {
		return nil
	}
	zfs := make([]zap.Field, 0, len(fields))
	for _, f := range fields {
		switch f.Type {
		case StringType:
			zfs = append(zfs, zap.String(f.Key, f.String))
		case Int64Type:
			zfs = append(zfs, zap.Int64(f.Key, f.Integer))
		case Int32Type:
			zfs = append(zfs, zap.Int32(f.Key, int32(f.Integer)))
		case Float64Type:
			zfs = append(zfs, zap.Float64(f.Key, f.Float))
		case BoolType:
			zfs = append(zfs, zap.Bool(f.Key, f.Integer == 1))
		case DurationType:
			zfs = append(zfs, zap.Duration(f.Key, time.Duration(f.Integer)))
		case TimeType:
			zfs = append(zfs, zap.String(f.Key, f.Interface.(time.Time).Format("2006-01-02 15:04:05.000")))
		case ErrorType:
			if f.Interface != nil {
				zfs = append(zfs, zap.NamedError(f.Key, f.Interface.(error)))
			}
		case AnyType:
			zfs = append(zfs, zap.Any(f.Key, f.Interface))
		}
	}
	return zfs
}
