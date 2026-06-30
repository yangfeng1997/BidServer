// Package ratelimit 提供令牌桶和滑动窗口限流器，可集成到 pipeline 中间件。
package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Limiter 限流器接口。
type Limiter interface {
	// Allow 检查是否允许本次请求通过。
	// key 用于区分不同用户/路由，为空时表示全局限流。
	Allow(key string) bool
}

// ErrRateLimited 限流触发时的哨兵错误。
var ErrRateLimited = errors.New("请求过于频繁，已被限流")

// ——— 令牌桶 ———

// TokenBucket 基于令牌桶算法的限流器。
// 以固定速率生成令牌，令牌数达到 cap 后停止生成。
// 每次 Allow 消耗 1 个令牌，无令牌时拒绝。
type TokenBucket struct {
	mu    sync.Mutex
	rate  float64 // 每秒生成的令牌数
	cap   float64 // 桶容量（允许的最大突发）
	tokens float64 // 当前令牌数
	last  time.Time // 上次补充时间
}

// NewTokenBucket 创建令牌桶限流器。
// rate: 每秒生成的令牌数；cap: 最大突发令牌数。cap 为 0 时取 rate。
func NewTokenBucket(rate float64, cap float64) *TokenBucket {
	if cap <= 0 {
		cap = rate
	}
	return &TokenBucket{
		rate:  rate,
		cap:   cap,
		tokens: cap,
		last:  time.Now(),
	}
}

// Allow 检查是否允许通过。不区分 key，全局共享一个桶。
func (tb *TokenBucket) Allow(_ string) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.last).Seconds()
	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.cap {
		tb.tokens = tb.cap
	}
	tb.last = now

	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

// ——— 滑动窗口 ———

// SlidingWindow 基于滑动窗口计数的限流器。
type SlidingWindow struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]*windowEntry
}

type windowEntry struct {
	count int
	reset time.Time
}

// NewSlidingWindow 创建滑动窗口限流器。
// limit: 每个窗口内允许的最大请求数。
func NewSlidingWindow(limit int, window time.Duration) *SlidingWindow {
	return &SlidingWindow{
		limit:   limit,
		window:  window,
		buckets: make(map[string]*windowEntry),
	}
}

// Allow 检查指定 key 是否允许通过。
func (sw *SlidingWindow) Allow(key string) bool {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	now := time.Now()

	entry, ok := sw.buckets[key]
	if !ok || now.After(entry.reset) {
		sw.buckets[key] = &windowEntry{count: 1, reset: now.Add(sw.window)}
		return true
	}

	if entry.count >= sw.limit {
		return false
	}

	entry.count++
	return true
}

// ——— 管道中间件适配 ———

// HandlerMiddleware 将限流器包装为 pipeline 前置钩子。
// keyFunc 从 context 中提取限流 key（如 uid、IP）。返回空字符串时使用全局限流。
func HandlerMiddleware(limiter Limiter, keyFunc func(ctx context.Context) string) func(ctx context.Context, in interface{}) (context.Context, interface{}, error) {
	return func(ctx context.Context, in interface{}) (context.Context, interface{}, error) {
		key := ""
		if keyFunc != nil {
			key = keyFunc(ctx)
		}
		if !limiter.Allow(key) {
			return ctx, in, ErrRateLimited
		}
		return ctx, in, nil
	}
}
