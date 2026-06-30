// Package logger 提供基于 zap 的全局日志器。
//
// 设计参照 Pitaya / Nano / MQant / Leaf / Cherry 的共同模式：
// 全局单例 + 包级便捷函数。组件无需注入 logger，直接调用 logger.Info() 等即可。
//
// 启动时可通过 SetLogger 替换默认实现，通常在 app.Builder.Build() 中自动完成。
package logger

import "go.uber.org/zap"

// ——— 全局默认 Logger ———

var defaultLogger *zap.SugaredLogger

func init() {
	defaultLogger = NewProduction("app")
}

// SetLogger 替换全局默认 Logger。传 nil 会被忽略。
func SetLogger(l *zap.SugaredLogger) {
	if l != nil {
		defaultLogger = l
	}
}

// GetLogger 返回全局默认 Logger。
func GetLogger() *zap.SugaredLogger {
	return defaultLogger
}

// ——— 包级便捷函数，委托给全局默认 Logger ———

func Debug(args ...interface{}) {
	defaultLogger.Debug(args...)
}

func Debugf(template string, args ...interface{}) {
	defaultLogger.Debugf(template, args...)
}

func Debugw(msg string, keysAndValues ...interface{}) {
	defaultLogger.Debugw(msg, keysAndValues...)
}

func Info(args ...interface{}) {
	defaultLogger.Info(args...)
}

func Infof(template string, args ...interface{}) {
	defaultLogger.Infof(template, args...)
}

func Infow(msg string, keysAndValues ...interface{}) {
	defaultLogger.Infow(msg, keysAndValues...)
}

func Warn(args ...interface{}) {
	defaultLogger.Warn(args...)
}

func Warnf(template string, args ...interface{}) {
	defaultLogger.Warnf(template, args...)
}

func Warnw(msg string, keysAndValues ...interface{}) {
	defaultLogger.Warnw(msg, keysAndValues...)
}

func Error(args ...interface{}) {
	defaultLogger.Error(args...)
}

func Errorf(template string, args ...interface{}) {
	defaultLogger.Errorf(template, args...)
}

func Errorw(msg string, keysAndValues ...interface{}) {
	defaultLogger.Errorw(msg, keysAndValues...)
}

func DPanic(args ...interface{}) {
	defaultLogger.DPanic(args...)
}

func DPanicf(template string, args ...interface{}) {
	defaultLogger.DPanicf(template, args...)
}

func DPanicw(msg string, keysAndValues ...interface{}) {
	defaultLogger.DPanicw(msg, keysAndValues...)
}

func Panic(args ...interface{}) {
	defaultLogger.Panic(args...)
}

func Panicf(template string, args ...interface{}) {
	defaultLogger.Panicf(template, args...)
}

func Panicw(msg string, keysAndValues ...interface{}) {
	defaultLogger.Panicw(msg, keysAndValues...)
}

func Fatal(args ...interface{}) {
	defaultLogger.Fatal(args...)
}

func Fatalf(template string, args ...interface{}) {
	defaultLogger.Fatalf(template, args...)
}

func Fatalw(msg string, keysAndValues ...interface{}) {
	defaultLogger.Fatalw(msg, keysAndValues...)
}

// ——— 派生 Logger ———

// Named 从全局 Logger 派生一个带名称的子 Logger。
func Named(name string) *zap.SugaredLogger {
	return defaultLogger.Named(name)
}

// With 从全局 Logger 派生一个带预设字段的 Logger。
func With(args ...interface{}) *zap.SugaredLogger {
	return defaultLogger.With(args...)
}

// Sync 刷新全局 Logger 的缓冲区。
func Sync() error {
	return defaultLogger.Sync()
}

// ——— 工厂函数 ———

// NewProduction 返回生产环境的 SugaredLogger（JSON 输出，info 级别）。
// name 作为 logger 名称，在每行日志中显示为 "logger" 字段。
func NewProduction(name string) *zap.SugaredLogger {
	base, err := zap.NewProduction(zap.AddCallerSkip(1))
	if err != nil {
		return zap.NewNop().Sugar()
	}
	return base.Sugar().Named(name)
}

// NewDevelopment 返回开发环境的 SugaredLogger（控制台输出，debug 级别）。
func NewDevelopment(name string) *zap.SugaredLogger {
	base, err := zap.NewDevelopment(zap.AddCallerSkip(1))
	if err != nil {
		return zap.NewNop().Sugar()
	}
	return base.Sugar().Named(name)
}
