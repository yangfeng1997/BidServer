// Package worker 提供异步任务协程池，支持重试与指数退避。
package worker

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"projectbid/server/logger"
)

// TaskFunc 异步任务函数签名。
type TaskFunc func(ctx context.Context) error

// RetryConfig 重试配置。
type RetryConfig struct {
	MaxRetries    int           // 最大重试次数，0 表示不重试
	InitialDelay  time.Duration // 首次重试延迟
	MaxDelay      time.Duration // 最大重试延迟上限
	BackoffFactor float64       // 退避倍数，默认 2
	Jitter        float64       // 随机抖动系数，0.1 表示 ±10%
}

// DefaultRetryConfig 返回默认重试配置。
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:    3,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      10 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        0.1,
	}
}

// Worker 异步任务协程池。
type Worker struct {
	name        string
	concurrency int
	taskQueue   chan *task
	wg          sync.WaitGroup
	running     int32
}

type task struct {
	fn   TaskFunc
	cfg  RetryConfig
	done chan error
}

// New 创建协程池。
// concurrency 为并行协程数，queueSize 为队列缓冲大小。
func New(name string, concurrency, queueSize int) *Worker {
	if concurrency <= 0 {
		concurrency = 1
	}
	if queueSize <= 0 {
		queueSize = concurrency * 100
	}
	return &Worker{
		name:        name,
		concurrency: concurrency,
		taskQueue:   make(chan *task, queueSize),
	}
}

// Start 启动协程池。
func (w *Worker) Start() {
	atomic.StoreInt32(&w.running, 1)
	for i := 0; i < w.concurrency; i++ {
		w.wg.Add(1)
		go w.workerLoop(i)
	}
	logger.Infow("Worker 协程池已启动", "名称", w.name, "并发数", w.concurrency)
}

// Stop 优雅停止协程池（等待所有已提交任务完成）。
func (w *Worker) Stop() {
	atomic.StoreInt32(&w.running, 0)
	close(w.taskQueue)
	w.wg.Wait()
	logger.Infow("Worker 协程池已停止", "名称", w.name)
}

// Enqueue 提交任务，不重试。阻塞直到任务被接收。
func (w *Worker) Enqueue(ctx context.Context, fn TaskFunc) error {
	return w.EnqueueWithRetry(ctx, fn, RetryConfig{MaxRetries: 0})
}

// EnqueueWithRetry 提交任务并配置重试策略。
func (w *Worker) EnqueueWithRetry(ctx context.Context, fn TaskFunc, cfg RetryConfig) error {
	if atomic.LoadInt32(&w.running) == 0 {
		return fmt.Errorf("worker %s 未启动", w.name)
	}

	done := make(chan error, 1)
	t := &task{fn: fn, cfg: cfg, done: done}

	select {
	case w.taskQueue <- t:
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) workerLoop(id int) {
	defer w.wg.Done()
	for t := range w.taskQueue {
		w.executeTask(t)
	}
}

func (w *Worker) executeTask(t *task) {
	var err error
	attempt := 0
	maxAttempts := t.cfg.MaxRetries + 1

	for attempt < maxAttempts {
		err = safeCall(t.fn)
		if err == nil {
			break
		}
		attempt++
		if attempt >= maxAttempts {
			break
		}
		delay := retryDelay(t.cfg, attempt)
		logger.Warnw("任务失败，将重试",
			"尝试", attempt,
			"最大", maxAttempts,
			"延迟", delay,
			"错误", err,
		)
		time.Sleep(delay)
	}
	t.done <- err
}

func retryDelay(cfg RetryConfig, attempt int) time.Duration {
	delay := float64(cfg.InitialDelay) * math.Pow(cfg.BackoffFactor, float64(attempt-1))
	if delay > float64(cfg.MaxDelay) {
		delay = float64(cfg.MaxDelay)
	}
	// 添加随机抖动
	if cfg.Jitter > 0 {
		jitter := delay * cfg.Jitter * (2*rand.Float64() - 1)
		delay += jitter
	}
	if delay < 0 {
		delay = float64(cfg.InitialDelay)
	}
	return time.Duration(delay)
}

func safeCall(fn TaskFunc) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("任务 panic: %v", r)
			logger.Errorw("Worker 任务 panic", "panic", r)
		}
	}()
	return fn(context.Background())
}
