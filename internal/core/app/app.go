package app

import (
	"errors"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"project/internal/core/logger"
)

type App interface {
	Startup() error
	Shutdown()
	Reload() error
	DieChan() chan bool
	DieNotifyChan() <-chan bool
	IsRunning() bool
	IsDaemon() bool
	RegisterModule(module Module) error
	GetModule(name string) (Module, error)
}

type BaseApp struct {
	dieChan       chan bool      // 进程内调用 Shutdown 后，会写入
	sigChan       chan os.Signal // 进程收到信号后会写入
	dieNotifyChan chan bool

	running       bool
	daemon        bool
	pprof         bool
	pprofAddr     string
	modulesMap    map[string]Module
	modulesArray  []moduleWrapper
	shutdownHooks []func()
	reloadHooks   []func() error
}

func NewBaseApp(dieChan chan bool, daemon bool, pprof bool, pprofAddr string, shutdownHooks []func(), reloadHooks []func() error) *BaseApp {
	if dieChan == nil {
		dieChan = make(chan bool)
	}

	return &BaseApp{
		dieChan:       dieChan,
		dieNotifyChan: make(chan bool, 1),
		sigChan:       make(chan os.Signal, 1),
		daemon:        daemon,
		pprof:         pprof,
		pprofAddr:     pprofAddr,
		modulesMap:    make(map[string]Module),
		shutdownHooks: append([]func(){}, shutdownHooks...),
		reloadHooks:   append([]func() error{}, reloadHooks...),
	}
}

func (app *BaseApp) Startup() error {
	if app.IsRunning() {
		logger.Main.Error("app startup failed, app has running")
		return errors.New("app has running")
	}

	logger.Main.Info("-------------------- app startup --------------------")
	logger.Main.Info("app start banner!")
	logger.Main.Info("-------------------------------- --------------------")

	app.startPprof()

	// 提前注入 app
	for _, wrapper := range app.modulesArray {
		wrapper.module.Set(app)
	}
	logger.Main.Info("app moudles Set ok!")

	// 调用各个module的Init
	for _, wrapper := range app.modulesArray {
		if err := wrapper.module.Init(); err != nil {
			return fmt.Errorf("init module %s: %w", wrapper.name, err)
		}
	}
	logger.Main.Info("app moudles Init ok!")

	for _, wrapper := range app.modulesArray {
		if err := wrapper.module.AfterInit(); err != nil {
			return fmt.Errorf("after init module %s: %w", wrapper.name, err)
		}
	}
	logger.Main.Info("app moudles AfterInit ok!")

	app.running = true
	// 进入时间循环
	app.runLoop()
	app.running = false
	logger.Main.Info("-------------------- app will shutdown --------------------")

	// 外部/业务代码调用 app.Shutdown()，会 close(app.dieChan)
	// 然后 Startup() 里的 <-app.dieChan 会因为 chan 被关闭而立即返回向下执行
	// 如果 dieChan 已经关闭：case <-app.dieChan 立即命中，什么都不做，避免重复 close panic
	// 如果 dieChan 还没关闭且没有值：走 default，执行 close(app.dieChan)，上面的 select 中会把值读取
	app.Shutdown()
	app.shutdownAllModules()
	app.shutdownAllHooks()
	// 通知没有注册成 module 的 goroutine，close 一个 chan，会广播给所有关注的 goroutine
	// 触发来源单一，可直接关闭
	close(app.dieNotifyChan)

	logger.Main.Info("-------------------- app has been shutdown --------------------")
	return nil
}

func (app *BaseApp) startPprof() {
	if !app.pprof {
		return
	}
	addr := app.pprofAddr
	if addr == "" {
		addr = "127.0.0.1:6060"
	}

	server := &http.Server{Addr: addr, Handler: nil}
	// shutdown hook 注册规则：谁创建资源，谁负责注册关闭逻辑。
	// pprof server 由 BaseApp 在这里创建，所以关闭 hook 也在这里注册；
	// builder 创建的基础设施在 builder 注册，module 自己创建的业务资源放到 module.Shutdown。
	app.shutdownHooks = append(app.shutdownHooks, func() {
		if err := server.Close(); err != nil && err != http.ErrServerClosed {
			logger.Main.Error("close pprof server failed", logger.Err(err))
		}
	})

	go func() {
		logger.Main.Info("pprof server start", logger.String("addr", addr))
		// Handler 为 nil 时使用默认路由器；http/pprof 的 init 会注册到 http.DefaultServeMux
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Main.Error("pprof server stopped", logger.Err(err))
		}
	}()
}

func (app *BaseApp) runLoop() {
	signal.Notify(app.sigChan, appSignals()...)
	defer signal.Stop(app.sigChan)
wait:
	for {
		select {
		case <-app.dieChan:
			logger.Main.Warn("app dieChan shutdown")
			// 内部主动停服，进入普通停服流程
			break wait
		case sig := <-app.sigChan:
			logger.Main.Warn("app sigChan", logger.String("signal", sig.String()))
			switch {
			case isShutdownSignal(sig):
				// 收到普通停服信号，进入普通停服流程
				break wait
			case isDrainShutdownSignal(sig):
				// 收到优雅停服信号，先停止接入，再等待存量逻辑结束
				break wait
			case isReloadSignal(sig):
				// 收到热更信号，执行热更流程；当前先不处理热更逻辑
				continue
			default:
				// 未识别信号，当前按普通停服流程处理
				break wait
			}
		}
	}
}

func (app *BaseApp) Shutdown() {
	select {
	case <-app.dieChan:
	default:
		close(app.dieChan)
	}
}

func (app *BaseApp) shutdownAllHooks() {
	for i := len(app.shutdownHooks) - 1; i >= 0; i-- {
		app.shutdownHooks[i]()
	}
}

func (app *BaseApp) shutdownAllModules() {
	for i := len(app.modulesArray) - 1; i >= 0; i-- {
		app.modulesArray[i].module.BeforeShutdown()
	}

	for i := len(app.modulesArray) - 1; i >= 0; i-- {
		app.modulesArray[i].module.Shutdown()
	}
}

func (app *BaseApp) Reload() error {
	reloadHooks := append([]func() error{}, app.reloadHooks...)

	for _, hook := range reloadHooks {
		if err := hook(); err != nil {
			return fmt.Errorf("reload hook: %w", err)
		}
	}
	return nil
}

func (app *BaseApp) DieChan() chan bool {
	return app.dieChan
}

func (app *BaseApp) DieNotifyChan() <-chan bool {
	return app.dieNotifyChan
}

func (app *BaseApp) IsRunning() bool {
	return app.running
}

func (app *BaseApp) IsDaemon() bool {
	return app.daemon
}

func (app *BaseApp) RegisterModule(module Module) error {
	name, err := app.validateModule(module)
	if err != nil {
		return err
	}

	app.modulesMap[name] = module
	app.modulesArray = append(app.modulesArray, moduleWrapper{
		module: module,
		name:   name,
	})
	return nil
}

func (app *BaseApp) GetModule(name string) (Module, error) {
	if module, ok := app.modulesMap[name]; ok {
		return module, nil
	}
	return nil, fmt.Errorf("module %s not found", name)
}

func (app *BaseApp) validateModule(module Module) (string, error) {
	if module == nil {
		return "", errors.New("module is nil")
	}
	name := module.Name()
	if name == "" {
		return "", errors.New("module name is empty")
	}
	if _, ok := app.modulesMap[name]; ok {
		return "", fmt.Errorf("module %s already registered", name)
	}
	return name, nil
}
