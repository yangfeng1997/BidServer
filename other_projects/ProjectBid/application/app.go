package application

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"time"

	"projectbid/server/cluster"
	"projectbid/server/component"
	"projectbid/server/config"
	"projectbid/server/acceptor"
	"projectbid/server/discovery"
	"projectbid/server/errors"
	"projectbid/server/logger"
	"projectbid/server/pipeline"
	"projectbid/server/service"
	"projectbid/server/session"
	"projectbid/server/timer"
)

// Application 管理所有 Service 和 Module 的完整生命周期。
type Application struct {
	config config.Config

	mu       sync.RWMutex
	stateVal State
	running  int32

	isFrontend bool
	serverID   string

	services []component.Component
	modules  []component.Component

	startTime   time.Time
	shutdownSig chan struct{} // 关闭信号
	dieChan     chan struct{} // 完全停止后关闭

	onStartup  []func() // 应用完全启动后执行的回调
	onShutdown []func() // 关闭前执行的回调

	forceExit int32

	// 网络层
	acceptors         []acceptor.Acceptor
	handlerService    *service.HandlerService
	remoteHooks       *pipeline.RemoteHooks
	remoteHandlerPool *service.HandlerPool
	sessionPool       session.SessionPool

	// Handler 组件注册（对齐 Pitaya: Build() 后 Register，Start() 前完成注册）
	handlerComponents []handlerReg
	remoteComponents  []handlerReg

	// 集群
	natsClient *cluster.NatsRPCClient
	natsServer *cluster.NatsRPCServer
	discovery  *discovery.EtcdDiscovery

	// 时间轮
	enableTimeWheel bool
	timeWheelTick   time.Duration
	timeWheelSize   int64
	timeWheel       *timer.TimeWheel
}

// ——— 公共方法 ———

// Start 运行完整的应用生命周期，阻塞直到关闭完成。
func (a *Application) Start() error {
	a.startTime = time.Now()

	// 注册信号监听
	sg := make(chan os.Signal, 1)
	signal.Notify(sg, a.config.Signals...)

	if err := a.startup(); err != nil {
		return err
	}

	// 触发启动完成钩子
	for _, fn := range a.onStartup {
		safeCall(fn)
	}

	// 等待关闭信号：程序化关闭或操作系统信号
	select {
	case <-a.shutdownSig:
	case sig := <-sg:
		logger.Infow("收到系统信号，开始关闭", "信号", sig.String())
	}

	a.shutdown()
	_ = logger.Sync()
	return nil
}

// Shutdown 发起优雅关闭。如果已在关闭中，设置强制退出标记。
func (a *Application) Shutdown() {
	a.mu.Lock()
	if a.stateVal == StateStopping {
		a.mu.Unlock()
		atomic.StoreInt32(&a.forceExit, 1)
		return
	}
	if a.stateVal == StateStopped {
		a.mu.Unlock()
		return
	}
	a.stateVal = StateStopping
	a.mu.Unlock()

	close(a.shutdownSig)
}

// State 返回当前生命周期状态。
func (a *Application) State() State {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.stateVal
}

// IsRunning 返回应用是否正在运行。
func (a *Application) IsRunning() bool {
	return atomic.LoadInt32(&a.running) == 1
}

// StartTime 返回 Start 被调用的时间。
func (a *Application) StartTime() time.Time {
	return a.startTime
}

// Name / Version 返回应用名称和版本。
func (a *Application) Name() string    { return a.config.Name }
func (a *Application) Version() string { return a.config.Version }

// Register 注册一个组件，其导出的 handler 方法将由反射自动发现。
// 必须在 Start() 之前调用（对齐 Pitaya 的 App.Register 模式）。
func (a *Application) Register(comp component.Component, opts ...service.Option) error {
	if a.handlerService == nil {
		return fmt.Errorf("handlerService 未初始化（前端服务需启用 acceptor）")
	}
	return a.handlerService.Register(comp, opts)
}

// RegisterRemote 注册一个远程组件，其 handler 方法仅供其他服务器通过 RPC 调用。
// 必须在 Start() 之前调用（对齐 Pitaya 的 App.RegisterRemote 模式）。
func (a *Application) RegisterRemote(comp component.Component, opts ...service.Option) error {
	if a.remoteHandlerPool == nil {
		return fmt.Errorf("remoteHandlerPool 未初始化（需配置 NATS/etcd）")
	}
	svc := service.NewService(comp, opts)
	if err := svc.ExtractHandler(); err != nil {
		return fmt.Errorf("提取远程 handler 失败: %w", err)
	}
	for name, handler := range svc.Handlers {
		a.remoteHandlerPool.Register(svc.Name, name, handler)
	}
	return nil
}

// IsFrontend 返回该服务是否为前端服务（面向客户端连接）。
func (a *Application) IsFrontend() bool { return a.isFrontend }

// ServerID 返回服务器唯一标识。
func (a *Application) ServerID() string { return a.serverID }

// Wait 返回一个 channel，在应用完全停止后关闭。
func (a *Application) Wait() <-chan struct{} {
	return a.dieChan
}

// ——— 内部生命周期 ———

func (a *Application) startup() error {
	a.mu.Lock()
	a.stateVal = StateInitializing
	a.mu.Unlock()

	logger.Infow("应用启动中", "名称", a.config.Name, "版本", a.config.Version)

	// 阶段零：启动时间轮
	if a.enableTimeWheel {
		a.timeWheel = timer.NewTimeWheel(a.timeWheelTick, a.timeWheelSize)
		if a.timeWheel != nil {
			a.timeWheel.Start()
			logger.Infow("时间轮已启动", "滴答", a.timeWheelTick, "槽数", a.timeWheelSize)
		}
	}

	// 阶段一：初始化 Service
	for _, svc := range a.services {
		if err := a.invokeInit(svc); err != nil {
			return &errors.StartupError{Component: svc.Name(), Err: err}
		}
	}
	// 阶段二：Service AfterInit
	for _, svc := range a.services {
		if err := a.invokeAfterInit(svc); err != nil {
			return &errors.StartupError{Component: svc.Name(), Err: err}
		}
	}
	// 阶段三：初始化 Module
	for _, mod := range a.modules {
		if err := a.invokeInit(mod); err != nil {
			return &errors.StartupError{Component: mod.Name(), Err: err}
		}
	}
	// 阶段四：Module AfterInit
	for _, mod := range a.modules {
		if err := a.invokeAfterInit(mod); err != nil {
			return &errors.StartupError{Component: mod.Name(), Err: err}
		}
	}

	// 阶段五：启动服务发现（注册服务）
	if a.discovery != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.discovery.Register(ctx); err != nil {
			return fmt.Errorf("注册服务发现失败: %w", err)
		}
	}

	// 阶段六：启动 NATS RPC 服务端（后端服务）
	if a.natsServer != nil {
		if err := a.natsServer.Listen(); err != nil {
			return fmt.Errorf("启动 NATS RPC 服务端失败: %w", err)
		}
	}

	// 阶段七：启动网络层（前端服务）
	if a.isFrontend && len(a.acceptors) > 0 {
		// 启动所有 acceptor
		for _, acc := range a.acceptors {
			if err := acc.ListenAndServe(); err != nil {
				return fmt.Errorf("启动网络监听失败: %w", err)
			}
			logger.Infow("网络监听已启动", "地址", acc.GetAddr())
		}

		// 启动消息分发协程
		go a.handlerService.Dispatch()

		// 为每个 acceptor 启动连接接受协程
		for _, acc := range a.acceptors {
			go a.acceptLoop(acc)
		}

		logger.Infow("网络层已启动", "监听器数量", len(a.acceptors))
	}

	a.mu.Lock()
	a.stateVal = StateRunning
	a.mu.Unlock()
	atomic.StoreInt32(&a.running, 1)

	logger.Infow("应用已启动", "服务数", len(a.services), "模块数", len(a.modules), "服务器ID", a.serverID)
	return nil
}

// acceptLoop 从 acceptor 接收新连接，为每个连接启动 handler。
func (a *Application) acceptLoop(acc acceptor.Acceptor) {
	for a.IsRunning() {
		conn, ok := <-acc.GetConnChan()
		if !ok {
			return
		}
		go a.handlerService.Handle(conn)
	}
}

func (a *Application) shutdown() {
	a.mu.Lock()
	a.stateVal = StateStopping
	a.mu.Unlock()

	atomic.StoreInt32(&a.running, 0)

	logger.Infow("应用关闭中", "超时", a.config.GracefulTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), a.config.GracefulTimeout)
	defer cancel()

	// 关闭所有活跃 session（踢掉已连接用户）
	a.sessionPool.CloseAll()

	// 关闭网络层（先停止接受新连接）
	for _, acc := range a.acceptors {
		if err := acc.Stop(); err != nil {
			logger.Warnw("停止网络监听失败", "错误", err)
		}
	}
	if len(a.acceptors) > 0 {
		logger.Info("网络监听已停止")
	}

	// 关闭 NATS RPC 服务端
	if a.natsServer != nil {
		if err := a.natsServer.Stop(); err != nil {
			logger.Warnw("停止 NATS RPC 服务端失败", "错误", err)
		}
	}

	// 从服务发现注销
	if a.discovery != nil {
		if err := a.discovery.Deregister(); err != nil {
			logger.Warnw("从 etcd 注销服务失败", "错误", err)
		}
	}

	// 关闭 NATS 客户端
	if a.natsClient != nil {
		_ = a.natsClient.Stop()
	}

	// 执行关闭前回调
	for _, fn := range a.onShutdown {
		safeCall(fn)
	}

	// 逆序关闭 Module
	a.shutdownGroup(ctx, a.modules)
	// 逆序关闭 Service
	a.shutdownGroup(ctx, a.services)

	// 停止时间轮
	if a.timeWheel != nil {
		a.timeWheel.Stop()
		logger.Info("时间轮已停止")
	}

	a.mu.Lock()
	a.stateVal = StateStopped
	a.mu.Unlock()

	close(a.dieChan)
	logger.Infow("应用已停止")
}

func (a *Application) shutdownGroup(ctx context.Context, items []component.Component) {
	for i := len(items) - 1; i >= 0; i-- {
		if atomic.LoadInt32(&a.forceExit) == 1 {
			logger.Warnw("强制退出，跳过剩余组件", "剩余", i+1)
			return
		}

		select {
		case <-ctx.Done():
			logger.Errorw("优雅关闭超时", "剩余", i+1)
			return
		default:
		}

		comp := items[i]
		logger.Debugw("停止组件", "名称", comp.Name())

		if err := safeInvoke(comp.Name(), "BeforeShutdown", comp.BeforeShutdown); err != nil {
			logger.Warnw("BeforeShutdown 错误", "名称", comp.Name(), "错误", err)
		}
		if err := safeInvoke(comp.Name(), "Shutdown", comp.Shutdown); err != nil {
			logger.Warnw("Shutdown 错误", "名称", comp.Name(), "错误", err)
		}
	}
}

// ——— 辅助方法 ———

func (a *Application) invokeInit(lc component.Component) error {
	return safeInvoke(lc.Name(), "Init", lc.Init)
}

func (a *Application) invokeAfterInit(lc component.Component) error {
	return safeInvoke(lc.Name(), "AfterInit", lc.AfterInit)
}

// safeInvoke 调用单个生命周期方法，带超时和 panic 恢复。
func safeInvoke(name, phase string, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorw("生命周期方法 panic", "组件", name, "阶段", phase, "panic", fmt.Sprintf("%v", r))
				errCh <- fmt.Errorf("%s.%s 发生 panic: %v", name, phase, r)
			}
		}()
		errCh <- fn(ctx)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			logger.Errorw("生命周期方法失败", "组件", name, "阶段", phase, "错误", err)
		}
		return err
	case <-ctx.Done():
		logger.Errorw("生命周期方法超时", "组件", name, "阶段", phase)
		return fmt.Errorf("%s.%s 超时: %w", name, phase, ctx.Err())
	}
}

// safeCall 运行无参回调，恢复 panic。
func safeCall(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorw("回调 panic", "panic", fmt.Sprintf("%v", r))
		}
	}()
	fn()
}

// ——— 错误码辅助 ———

// 重新导出错误码常量，方便外部统一引用。
const (
	PIT000 = errors.PIT000
	PIT400 = errors.PIT400
	PIT404 = errors.PIT404
	PIT408 = errors.PIT408
	PIT498 = errors.PIT498
	PIT499 = errors.PIT499
	PIT500 = errors.PIT500
)

// Error 用指定错误码包装 error，若 err 已有错误码则保留原码。
func Error(code string, err error) error {
	return errors.NewError(err, code)
}
