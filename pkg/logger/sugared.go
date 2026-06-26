package logger

import "go.uber.org/zap"

// SugaredLogger 便捷日志接口，适合低频路径（启动、关闭、错误处理）
//
// 性能参考（zap benchmark，仅供量级参考）
// - Debug/Info/Error（强类型 Field）：~100 ns，0 alloc  → 高频热路径首选
// - Debugw/Infow（松散 kv）         ：~200 ns，1 alloc  → 低频路径，key-value 结构化输出
// - Debugf/Infof（printf 格式化）   ：~300 ns，2 alloc  → 低频路径，拼接描述性文字
//
// 注意：Debugw/Infow 的 keysAndValues 必须严格 key-value 交替，key 必须是 string
// 否则 zap 运行时补 !BADKEY，不会编译报错
type SugaredLogger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)

	Debugw(msg string, keysAndValues ...any)
	Infow(msg string, keysAndValues ...any)
	Warnw(msg string, keysAndValues ...any)
	Errorw(msg string, keysAndValues ...any)
	Fatalw(msg string, keysAndValues ...any)

	// With 派生子 SugaredLogger，绑定固定 kv 字段
	With(keysAndValues ...any) SugaredLogger
}

// // zapSugared：包装 zap.SugaredLogger

type zapSugared struct {
	s *zap.SugaredLogger
}

func (z *zapSugared) Debugf(format string, args ...any) { z.s.Debugf(format, args...) }
func (z *zapSugared) Infof(format string, args ...any)  { z.s.Infof(format, args...) }
func (z *zapSugared) Warnf(format string, args ...any)  { z.s.Warnf(format, args...) }
func (z *zapSugared) Errorf(format string, args ...any) { z.s.Errorf(format, args...) }
func (z *zapSugared) Fatalf(format string, args ...any) { z.s.Fatalf(format, args...) }

func (z *zapSugared) Debugw(msg string, kv ...any) { z.s.Debugw(msg, kv...) }
func (z *zapSugared) Infow(msg string, kv ...any)  { z.s.Infow(msg, kv...) }
func (z *zapSugared) Warnw(msg string, kv ...any)  { z.s.Warnw(msg, kv...) }
func (z *zapSugared) Errorw(msg string, kv ...any) { z.s.Errorw(msg, kv...) }
func (z *zapSugared) Fatalw(msg string, kv ...any) { z.s.Fatalw(msg, kv...) }

func (z *zapSugared) With(kv ...any) SugaredLogger {
	return &zapSugared{s: z.s.With(kv...)}
}

// ---------- ZapBackend 扩展 ----------

// Sugar 从 ZapBackend 获取 SugaredLogger
func (b *ZapBackend) Sugar() SugaredLogger {
	return &zapSugared{s: b.z.Sugar()}
}

// ---------- 全局快捷访问 ----------

// Sugar 获取全局 SugaredLogger，每次从全局 Logger 派生，无需单独维护全局变量
// 若全局 Logger 底层不是 ZapBackend，返回 nopSugared（不输出）
func Sugar() SugaredLogger {
	if cl, ok := G().(*coreLogger); ok {
		if zb, ok := cl.backend.(*ZapBackend); ok {
			return zb.Sugar()
		}
	}
	return nopSugared{}
}

// ---------- nopSugared：兜底空实现 ----------

type nopSugared struct{}

func (nopSugared) Debugf(_ string, _ ...any)   {}
func (nopSugared) Infof(_ string, _ ...any)    {}
func (nopSugared) Warnf(_ string, _ ...any)    {}
func (nopSugared) Errorf(_ string, _ ...any)   {}
func (nopSugared) Fatalf(_ string, _ ...any)   {}
func (nopSugared) Debugw(_ string, _ ...any)   {}
func (nopSugared) Infow(_ string, _ ...any)    {}
func (nopSugared) Warnw(_ string, _ ...any)    {}
func (nopSugared) Errorw(_ string, _ ...any)   {}
func (nopSugared) Fatalw(_ string, _ ...any)   {}
func (nopSugared) With(_ ...any) SugaredLogger { return nopSugared{} }
