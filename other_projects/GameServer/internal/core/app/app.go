package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"project/pkg/taskqueue"
)

// App 管理模块生命周期和单线程主循环
type App struct {
	infraModules []Module
	modules      []Module
	q            *taskqueue.Queue
	tick         time.Duration
	readyTimeout time.Duration
	drainTimeout time.Duration
	quit         chan struct{}
	stopped      chan struct{}
	stopOnce     sync.Once
}

// New 从 BaseOptions 创建 App，infra 模块由 runtime 注入以避免 import cycle
func New(opt *BaseOptions) *App {
	if opt == nil {
		opt = &BaseOptions{}
		opt.Defaults()
	}
	return &App{
		q:            taskqueue.New(0),
		tick:         opt.Tick,
		readyTimeout: opt.ReadyTimeout,
		drainTimeout: opt.DrainTimeout,
		quit:         make(chan struct{}),
		stopped:      make(chan struct{}),
	}
}

// RegisterInfra 注册基础设施模块（在业务模块前处理）
func (a *App) RegisterInfra(m Module) { a.infraModules = append(a.infraModules, m) }

func (a *App) Register(m Module) { a.modules = append(a.modules, m) }

func (a *App) Post(fn func()) { a.q.Post(fn) }

func GetModule[T Module](a *App) T {
	for _, m := range a.infraModules {
		if v, ok := m.(T); ok {
			return v
		}
	}
	for _, m := range a.modules {
		if v, ok := m.(T); ok {
			return v
		}
	}
	var zero T
	panic(fmt.Sprintf("app.GetModule: module %T not found", zero))
}

func (a *App) Init() error {
	all := a.allModules()
	for _, m := range all {
		if err := m.Init(a); err != nil {
			return fmt.Errorf("module %T Init: %w", m, err)
		}
	}
	for _, m := range all {
		if err := m.AfterInit(); err != nil {
			return fmt.Errorf("module %T AfterInit: %w", m, err)
		}
	}

	if a.readyTimeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), a.readyTimeout)
		defer cancel()
		for _, m := range all {
			if w, ok := m.(ReadyWaiter); ok {
				if err := w.WaitReady(ctx); err != nil {
					return fmt.Errorf("module %T WaitReady: %w", m, err)
				}
			}
		}
	}
	return nil
}

func (a *App) Run() error {
	defer a.Fini()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		select {
		case <-sigCh:
			if a.drainTimeout > 0 {
				time.Sleep(a.drainTimeout)
			}
			a.closeQuit()
		case <-a.stopped:
		}
	}()

	a.runLoop(a.allModules())
	return nil
}

func (a *App) Fini() {
	a.closeQuit()
	a.stopOnce.Do(func() {
		all := a.allModules()
		for i := len(all) - 1; i >= 0; i-- {
			all[i].BeforeStop()
		}
		for i := len(all) - 1; i >= 0; i-- {
			all[i].Fini()
		}
		close(a.stopped)
	})
}

func (a *App) closeQuit() {
	select {
	case <-a.quit:
	default:
		close(a.quit)
	}
}

func (a *App) runLoop(modules []Module) {
	var ticker *time.Ticker
	if a.tick > 0 {
		ticker = time.NewTicker(a.tick)
		defer ticker.Stop()
	}

	for {
		if ticker == nil {
			select {
			case fn := <-a.q.C():
				fn()
			case <-a.quit:
				return
			}
			continue
		}

		select {
		case fn := <-a.q.C():
			fn()
		case <-ticker.C:
			for _, m := range modules {
				if u, ok := m.(Updater); ok {
					u.Update(a.tick)
				}
			}
		case <-a.quit:
			return
		}
	}
}

func (a *App) allModules() []Module {
	mods := make([]Module, 0, len(a.infraModules)+len(a.modules))
	mods = append(mods, a.infraModules...)
	mods = append(mods, a.modules...)
	return mods
}

var _ Poster = (*App)(nil)

type Poster interface {
	Post(fn func())
}
