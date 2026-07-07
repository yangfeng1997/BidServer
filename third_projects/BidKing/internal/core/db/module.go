package db

import (
	"sync"

	"project/internal/core/app"
)

// Job 表示一个异步 DB 操作
type Job struct {
	Key  string
	Fn   func() (any, error)
	Done func(any, error)
}

// worker goroutine 池
type Pool struct {
	workers int
	jobs    chan Job
	wg      sync.WaitGroup
	stopCh  chan struct{}
}

// 创建 DB worker 池
func NewPool(workers int) *Pool {
	if workers <= 0 {
		workers = 4
	}
	return &Pool{
		workers: workers,
		jobs:    make(chan Job, 256),
		stopCh:  make(chan struct{}),
	}
}

// 启动 worker goroutines
func (p *Pool) Start() {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
}

func (p *Pool) worker() {
	defer p.wg.Done()
	for {
		select {
		case <-p.stopCh:
			return
		case job, ok := <-p.jobs:
			if !ok {
				return
			}
			result, err := job.Fn()
			if job.Done != nil {
				job.Done(result, err)
			}
		}
	}
}

// 提交异步 DB 任务
func (p *Pool) Submit(job Job) {
	select {
	case <-p.stopCh:
		if job.Done != nil {
			job.Done(nil, ErrPoolClosed)
		}
		return
	default:
	}
	select {
	case <-p.stopCh:
		if job.Done != nil {
			job.Done(nil, ErrPoolClosed)
		}
	case p.jobs <- job:
	}
}

// 停止所有 worker
func (p *Pool) Stop() {
	select {
	case <-p.stopCh:
		return
	default:
		close(p.stopCh)
	}
	p.wg.Wait()
}

// ErrPoolClosed 表示 Pool 已关闭
var ErrPoolClosed = errPoolClosed{}

type errPoolClosed struct{}

func (e errPoolClosed) Error() string { return "db pool closed" }

// DB 模块
type Module struct {
	app.BaseModule
	pool   *Pool
	poster app.Poster
}

// 创建 DB 模块
func NewModule(workers int) *Module {
	return &Module{pool: NewPool(workers)}
}

func (m *Module) Init(a *app.App) error {
	m.poster = a
	return nil
}

func (m *Module) AfterInit() error {
	m.pool.Start()
	return nil
}

func (m *Module) BeforeStop() { m.pool.Stop() }
func (m *Module) Fini()       {}

// 发起异步 DB 操作
func (m *Module) Async(key string, fn func() (any, error), done func(any, error)) {
	m.pool.Submit(Job{
		Key: key,
		Fn:  fn,
		Done: func(result any, err error) {
			m.poster.Post(func() {
				done(result, err)
			})
		},
	})
}

// 返回底层 worker 池
func (m *Module) Pool() *Pool { return m.pool }
