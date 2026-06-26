package logger

import "fmt"

// Level 日志级别
type Level int8

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
	FatalLevel
)

func (l Level) String() string {
	switch l {
	case DebugLevel:
		return "debug"
	case InfoLevel:
		return "info"
	case WarnLevel:
		return "warn"
	case ErrorLevel:
		return "error"
	case FatalLevel:
		return "fatal"
	default:
		return "unknown"
	}
}

func (l Level) MarshalText() ([]byte, error) {
	return []byte(l.String()), nil
}

func (l *Level) UnmarshalText(text []byte) error {
	switch string(text) {
	case "debug":
		*l = DebugLevel
	case "info", "":
		*l = InfoLevel
	case "warn", "warning":
		*l = WarnLevel
	case "error":
		*l = ErrorLevel
	case "fatal":
		*l = FatalLevel
	default:
		return fmt.Errorf("unknown log level %q", text)
	}
	return nil
}

// Logger 业务代码依赖的核心接口，参数全部强类型，无 interface{} 可变参数
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	Fatal(msg string, fields ...Field)

	// With 派生子 Logger，携带固定字段（如 roomID、playerID）
	With(fields ...Field) Logger

	// IsEnabled 热路径 level 检查，避免构造昂贵字段后发现级别未开启
	IsEnabled(level Level) bool
}

// Backend 三方日志库适配器需要实现的内部接口
// 业务代码不直接依赖 Backend，只通过 Logger 使用
type Backend interface {
	Log(level Level, msg string, fields []Field)
	With(fields []Field) Backend
	IsEnabled(level Level) bool
}
