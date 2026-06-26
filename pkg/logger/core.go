package logger

// coreLogger 将 Logger 接口桥接到 Backend
type coreLogger struct {
	backend Backend
}

// New 用指定 Backend 创建 Logger
func New(b Backend) Logger {
	return &coreLogger{backend: b}
}

func (l *coreLogger) Debug(msg string, fields ...Field) {
	// Debug 通常在生产关闭，提前 check 避免 slice 传参开销
	if l.backend.IsEnabled(DebugLevel) {
		l.backend.Log(DebugLevel, msg, fields)
	}
}

func (l *coreLogger) Info(msg string, fields ...Field) {
	l.backend.Log(InfoLevel, msg, fields)
}

func (l *coreLogger) Warn(msg string, fields ...Field) {
	l.backend.Log(WarnLevel, msg, fields)
}

func (l *coreLogger) Error(msg string, fields ...Field) {
	l.backend.Log(ErrorLevel, msg, fields)
}

func (l *coreLogger) Fatal(msg string, fields ...Field) {
	l.backend.Log(FatalLevel, msg, fields)
}

func (l *coreLogger) With(fields ...Field) Logger {
	return &coreLogger{backend: l.backend.With(fields)}
}

func (l *coreLogger) IsEnabled(level Level) bool {
	return l.backend.IsEnabled(level)
}
