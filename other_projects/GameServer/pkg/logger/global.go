package logger

import "sync/atomic"

// global 包级默认 Logger，原子指针保证并发安全替换
var global atomic.Pointer[Logger]

// globalSugar 缓存全局 SugaredLogger，避免 Debugf/Debugw 每次调用重新分配包装对象
var globalSugar atomic.Pointer[SugaredLogger]

func init() {
	var l Logger = &nopLogger{}
	global.Store(&l)
	var s SugaredLogger = nopSugared{}
	globalSugar.Store(&s)
}

// SetGlobal 替换全局 Logger（通常在 main 初始化后调用一次）
func SetGlobal(l Logger) {
	global.Store(&l)
	s := Sugar()
	globalSugar.Store(&s)
}

// G 获取全局 Logger
func G() Logger {
	return *global.Load()
}

// s 获取缓存的全局 SugaredLogger
func s() SugaredLogger {
	return *globalSugar.Load()
}

// // 包级快捷函数，调用栈深度与直接调用一致
// (global.go:func Info → backend.Log = core.go:coreLogger.Info → backend.Log，均为 callerSkip=2)
//
// 性能选型
// - Debug/Info/Warn/Error/Fatal（强类型 Field）：零反射，编译期类型检查，适合高频热路径（帧循环、网络收发）
// // printf 格式化用于低频路径（启动、关闭、错误）
func Debug(msg string, fields ...Field) { emit(DebugLevel, msg, fields) }
func Info(msg string, fields ...Field)  { emit(InfoLevel, msg, fields) }
func Warn(msg string, fields ...Field)  { emit(WarnLevel, msg, fields) }
func Error(msg string, fields ...Field) { emit(ErrorLevel, msg, fields) }
func Fatal(msg string, fields ...Field) { emit(FatalLevel, msg, fields) }

func Debugf(format string, args ...any) { s().Debugf(format, args...) }
func Infof(format string, args ...any)  { s().Infof(format, args...) }
func Warnf(format string, args ...any)  { s().Warnf(format, args...) }
func Errorf(format string, args ...any) { s().Errorf(format, args...) }
func Fatalf(format string, args ...any) { s().Fatalf(format, args...) }

func Debugw(msg string, kv ...any) { s().Debugw(msg, kv...) }
func Infow(msg string, kv ...any)  { s().Infow(msg, kv...) }
func Warnw(msg string, kv ...any)  { s().Warnw(msg, kv...) }
func Errorw(msg string, kv ...any) { s().Errorw(msg, kv...) }
func Fatalw(msg string, kv ...any) { s().Fatalw(msg, kv...) }

// emit 是包级函数的公共快路径：*coreLogger 直接操作 backend，其余走接口调度
func emit(level Level, msg string, fields []Field) {
	l := *global.Load()
	if cl, ok := l.(*coreLogger); ok {
		if cl.backend.IsEnabled(level) {
			cl.backend.Log(level, msg, fields)
		}
		return
	}
	switch level {
	case DebugLevel:
		l.Debug(msg, fields...)
	case InfoLevel:
		l.Info(msg, fields...)
	case WarnLevel:
		l.Warn(msg, fields...)
	case ErrorLevel:
		l.Error(msg, fields...)
	case FatalLevel:
		l.Fatal(msg, fields...)
	}
}

func With(fields ...Field) Logger { return G().With(fields...) }

// ---------- nopLogger：不输出任何内容，用于测试或兜底 ----------

type nopLogger struct{}

func (n *nopLogger) Debug(_ string, _ ...Field) {}
func (n *nopLogger) Info(_ string, _ ...Field)  {}
func (n *nopLogger) Warn(_ string, _ ...Field)  {}
func (n *nopLogger) Error(_ string, _ ...Field) {}
func (n *nopLogger) Fatal(_ string, _ ...Field) {}
func (n *nopLogger) With(_ ...Field) Logger     { return n }
func (n *nopLogger) IsEnabled(_ Level) bool     { return false }
