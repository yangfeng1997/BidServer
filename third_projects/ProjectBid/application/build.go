package application

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"projectbid/server/agent"
	"projectbid/server/cluster"
	"projectbid/server/component"
	"projectbid/server/config"
	"projectbid/server/conn/message"
	"projectbid/server/acceptor"
	"projectbid/server/constants"
	"projectbid/server/discovery"
	"projectbid/server/logger"
	"projectbid/server/pipeline"
	"projectbid/server/serialize"
	"projectbid/server/service"
	"projectbid/server/session"
)

// ServiceOption 控制 Service 在 Builder 中的注册方式。
type ServiceOption func(*serviceEntry)

// WithServiceBefore 确保本 Service 在指定 Service 之前启动。
func WithServiceBefore(name string) ServiceOption {
	return func(e *serviceEntry) { e.before = append(e.before, name) }
}

// WithServiceAfter 确保本 Service 在指定 Service 之后启动。
func WithServiceAfter(name string) ServiceOption {
	return func(e *serviceEntry) { e.after = append(e.after, name) }
}

// ModuleOption 控制 Module 在 Builder 中的注册方式。
type ModuleOption func(*moduleEntry)

// WithModuleDependsOn 声明本 Module 依赖指定的 Module。
func WithModuleDependsOn(names ...string) ModuleOption {
	return func(e *moduleEntry) { e.dependsOn = append(e.dependsOn, names...) }
}

type handlerReg struct {
	comp component.Component
	opts []service.Option
}

type serviceEntry struct {
	comp   component.Component
	before []string
	after  []string
}

type moduleEntry struct {
	comp      component.Component
	dependsOn []string
}

// Builder 通过注册 Service 和 Module 来构建 Application。
type Builder struct {
	cfg    config.Config
	logger *zap.SugaredLogger

	isFrontend bool

	services []*serviceEntry
	modules  []*moduleEntry

	onStartup  []func()
	onShutdown []func()

	// 网络层配置
	acceptors       []acceptor.Acceptor
	serializer      serialize.Serializer
	acceptorOpts    acceptor.Options

	// 集群配置
	serverID   string

	// Handler 组件注册（移至 Application 上，对齐 Pitaya App.Register 模式）
	beforeHooks        []pipeline.HandlerTempl
	afterHooks         []pipeline.AfterHandlerTempl
	remoteBeforeHooks  []pipeline.HandlerTempl
	remoteAfterHooks   []pipeline.AfterHandlerTempl

		// RPC 调用方代理
		rpcCallers []rpcCallerEntry

	// 构建后暴露的核心对象
	handlerService *service.HandlerService
	handlerPool    *service.HandlerPool
		remoteHandlerPool *service.HandlerPool
	pipeHooks      *pipeline.HandlerHooks
	remoteHooks    *pipeline.RemoteHooks
	sessionPool    session.SessionPool
	agentFactory   agent.AgentFactory

}

// NewBuilder 用给定的函数式选项创建 Builder。
// isFrontend 标记该服务是否为前端服务（面向客户端）。
// 前端服务必须启用 acceptor 以接受客户端连接；后端服务通过 NATS 进行 RPC 通信。
func NewBuilder(isFrontend bool, opts ...config.Option) *Builder {
	cfg := config.Default()
	for _, o := range opts {
		o(&cfg)
	}
	return &Builder{
		cfg:              cfg,
		isFrontend:       isFrontend,
		serverID:         uuid.New().String(),
		handlerPool:      service.NewHandlerPool(),
		remoteHandlerPool: service.NewHandlerPool(),
		pipeHooks:        pipeline.NewHandlerHooks(),
		remoteHooks:      pipeline.NewRemoteHooks(),
		sessionPool:      session.NewSessionPool(),
	}
}

// AddService 注册一个基础设施 Service。
func (b *Builder) AddService(comp component.Component, opts ...ServiceOption) *Builder {
	e := &serviceEntry{comp: comp}
	for _, o := range opts {
		o(e)
	}
	b.services = append(b.services, e)
	return b
}

// AddModule 注册一个业务逻辑 Module。
func (b *Builder) AddModule(comp component.Component, opts ...ModuleOption) *Builder {
	e := &moduleEntry{comp: comp}
	for _, o := range opts {
		o(e)
	}
	b.modules = append(b.modules, e)
	return b
}

// SetLogger 设置应用 Logger。
func (b *Builder) SetLogger(l *zap.SugaredLogger) *Builder {
	b.logger = l
	return b
}

// OnStartup 注册在应用完全启动后执行的回调。
func (b *Builder) OnStartup(fn func()) *Builder {
	b.onStartup = append(b.onStartup, fn)
	return b
}

// OnShutdown 注册在关闭信号后执行的回调。
func (b *Builder) OnShutdown(fn func()) *Builder {
	b.onShutdown = append(b.onShutdown, fn)
	return b
}

// ——— Handler 组件 ———

// AddBeforeHandlerHook 添加前置管道钩子，在 handler 方法执行前调用。
func (b *Builder) AddBeforeHandlerHook(hook pipeline.HandlerTempl) *Builder {
	b.beforeHooks = append(b.beforeHooks, hook)
	return b
}

// AddAfterHandlerHook 添加后置管道钩子，在 handler 方法执行后调用。
func (b *Builder) AddAfterHandlerHook(hook pipeline.AfterHandlerTempl) *Builder {
	b.afterHooks = append(b.afterHooks, hook)
	return b
}

// AddBeforeRemoteHook 添加远程 RPC 前置钩子，在远程 handler 执行前调用。
func (b *Builder) AddBeforeRemoteHook(hook pipeline.HandlerTempl) *Builder {
	b.remoteBeforeHooks = append(b.remoteBeforeHooks, hook)
	return b
}

// AddAfterRemoteHook 添加远程 RPC 后置钩子，在远程 handler 执行后调用。
func (b *Builder) AddAfterRemoteHook(hook pipeline.AfterHandlerTempl) *Builder {
	b.remoteAfterHooks = append(b.remoteAfterHooks, hook)
	return b
}

// ——— 网络层 ———

// EnableAcceptor 添加一个网络监听器。可多次调用以同时监听 TCP 和 WS。
// 仅前端服务可调用；后端服务调用将直接 panic（框架层配置错误）。
func (b *Builder) EnableAcceptor(opts acceptor.Options) *Builder {
	if !b.isFrontend {
		panic("后端服务不允许调用 EnableAcceptor，请检查 isFrontend 配置")
	}
	b.acceptors = append(b.acceptors, acceptor.NewAcceptor(opts))
	b.acceptorOpts = opts
	b.serializer = opts.Serializer
	return b
}

// EnableNats 配置 NATS 连接（后端服务用于跨服 RPC，前端服务用于推送）。
func (b *Builder) EnableNats(url string) *Builder {
	b.cfg.Cluster.NatsURL = url
	return b
}

// EnableEtcd 配置 etcd 服务发现。
func (b *Builder) EnableEtcd(cfg discovery.EtcdConfig) *Builder {
	b.cfg.Cluster.Etcd = &cfg
	return b
}

// ——— 基础设施 ———

// EnableTimeWheel 启用分层时间轮。
func (b *Builder) EnableTimeWheel(tick time.Duration, wheelSize int64) *Builder {
	b.cfg.Timer.Enabled = true
	b.cfg.Timer.Tick = tick
	b.cfg.Timer.WheelSize = wheelSize
	return b
}

// ——— 访问器（供外部使用） ———

// GetSessionPool 返回会话池。
func (b *Builder) GetSessionPool() session.SessionPool { return b.sessionPool }

// GetHandlerService 返回 HandlerService 实例。
func (b *Builder) GetHandlerService() *service.HandlerService { return b.handlerService }

// GetHandlerPool 返回 HandlerPool 实例。
func (b *Builder) GetHandlerPool() *service.HandlerPool { return b.handlerPool }

// GetPipelineHooks 返回本地管道钩子。
func (b *Builder) GetPipelineHooks() *pipeline.HandlerHooks { return b.pipeHooks }

// GetRemoteHooks 返回远程管道钩子。
func (b *Builder) GetRemoteHooks() *pipeline.RemoteHooks { return b.remoteHooks }

// Build 验证注册的组件、解析顺序，返回可以启动的 Application。
func (b *Builder) Build() (*Application, error) {
	l := b.logger
	if l == nil {
		l = logger.NewProduction(b.cfg.Name)
	}
	logger.SetLogger(l)

	cfg := b.cfg
	if cfg.DisplayName == "" {
		cfg.DisplayName = cfg.Name
	}

	if err := b.validateNames(); err != nil {
		return nil, err
	}

	// 验证前后端配置一致性（框架层配置错误，直接 panic 暴露问题）
	if b.isFrontend && len(b.acceptors) == 0 {
		panic("前端服务必须至少启用一个 acceptor，请在 Builder 上调用 EnableAcceptor")
	}
	if !b.isFrontend && len(b.acceptors) > 0 {
		panic("后端服务不应启用 acceptor，请检查 isFrontend 配置")
	}

	// 验证 cluster 配置：后端服务必须配置 NATS 和 etcd；前端可选
	if !b.isFrontend {
		if b.cfg.Cluster.NatsURL == "" {
			panic("后端服务必须配置 NATS 连接，请调用 EnableNats")
		}
		if b.cfg.Cluster.Etcd == nil {
			panic("后端服务必须配置 etcd，请调用 EnableEtcd")
		}
	}

	services, err := b.resolveServices()
	if err != nil {
		return nil, fmt.Errorf("Service 顺序解析: %w", err)
	}

	modules, err := b.resolveModules()
	if err != nil {
		return nil, fmt.Errorf("Module 顺序解析: %w", err)
	}

	// 构建 handler pipeline hooks（本地消息）
	for _, hook := range b.beforeHooks {
		b.pipeHooks.BeforeHandler.PushBack(hook)
	}
	for _, hook := range b.afterHooks {
		b.pipeHooks.AfterHandler.PushBack(hook)
	}

	// 构建 remote pipeline hooks（跨服 RPC 消息）
	for _, hook := range b.remoteBeforeHooks {
		b.remoteHooks.BeforeHandler.PushBack(hook)
	}
	for _, hook := range b.remoteAfterHooks {
		b.remoteHooks.AfterHandler.PushBack(hook)
	}

	// 创建 Agent 工厂和 HandlerService（前端服务）
	if len(b.acceptors) > 0 {
		messageEncoder := b.acceptorOpts.MessageEncoder
		if messageEncoder == nil {
			messageEncoder = message.NewMessagesEncoder(false)
		}
		b.agentFactory = agent.NewAgentFactory(
			b.acceptorOpts.PacketDecoder,
			b.acceptorOpts.PacketEncoder,
			b.serializer,
			b.acceptorOpts.HeartbeatTimeout,
			b.acceptorOpts.WriteTimeout,
			messageEncoder,
			b.acceptorOpts.MessagesBufferSize,
			b.sessionPool,
		)

		b.handlerService = service.NewHandlerService(
			b.acceptorOpts.PacketDecoder,
			b.serializer,
			cfg.Buffer.LocalProcessBufferSize,
			b.agentFactory,
			b.pipeHooks,
			b.handlerPool,
			cfg.Name,
		)

	}

	// 创建集群通信组件
	var natsClient *cluster.NatsRPCClient
	var natsServer *cluster.NatsRPCServer
	var sd *discovery.EtcdDiscovery

	if b.cfg.Cluster.NatsURL != "" {
		var err error
		natsClient, err = cluster.NewNatsRPCClient(cluster.NatsRPCClientConfig{
			URL:      b.cfg.Cluster.NatsURL,
			ServerID: b.serverID,
		})
		if err != nil {
			return nil, fmt.Errorf("创建 NATS RPC 客户端失败: %w", err)
		}

			if b.handlerService != nil {
				b.handlerService.SetNATSClient(natsClient)
			}
			if err := wireRPCCallers(b, natsClient); err != nil {
				return nil, fmt.Errorf("RPC 调用方代理绑定失败: %w", err)
			}
	}

	if b.cfg.Cluster.Etcd != nil {
		svInfo := cluster.NewServer(b.serverID, cfg.Name, b.isFrontend, nil)
		var err error
		sd, err = discovery.NewEtcdDiscovery(*b.cfg.Cluster.Etcd, svInfo)
		if err != nil {
			return nil, fmt.Errorf("创建 etcd 服务发现失败: %w", err)
		}
	}

	// 创建 NATS RPC 服务端（后端监听 RPC 请求，前端监听推送/踢下线）
	if b.cfg.Cluster.NatsURL != "" && b.cfg.Cluster.Etcd != nil {
		// 确保有序列化器（后端未调用 EnableAcceptor 时无 serializer）
		ser := b.serializer
		if ser == nil {
			ser = &serialize.JSONSerializer{}
		}

			// RemoteHooks 转换为 HandlerHooks（两者嵌入同一个 Hooks 基结构体）
			remoteHooks := &pipeline.HandlerHooks{
				Hooks: pipeline.Hooks{
					BeforeHandler: b.remoteHooks.BeforeHandler,
					AfterHandler:  b.remoteHooks.AfterHandler,
				},
			}
			remoteService := service.NewRemoteService(b.remoteHandlerPool, ser, remoteHooks, b.sessionPool, natsClient, sd)

		var err error
		natsServer, err = cluster.NewNatsRPCServer(cluster.NatsRPCServerConfig{
			URL:        b.cfg.Cluster.NatsURL,
			ServerID:   b.serverID,
			ServerType: cfg.Name,
		})
		if err != nil {
			return nil, fmt.Errorf("创建 NATS RPC 服务端失败: %w", err)
		}
		natsServer.SetHandler(remoteService)
	}

	return &Application{
		config:      cfg,
		isFrontend:  b.isFrontend,
		serverID:    b.serverID,
		services:    services,
		modules:     modules,
		shutdownSig: make(chan struct{}),
		dieChan:     make(chan struct{}),
		onStartup:   b.onStartup,
		onShutdown:  b.onShutdown,
		// 网络层
		acceptors:      b.acceptors,
		handlerService: b.handlerService,
		remoteHooks:    b.remoteHooks,
		sessionPool:    b.sessionPool,
		// 集群
		natsClient: natsClient,
		natsServer: natsServer,
		discovery:  sd,
		// 时间轮
		enableTimeWheel: cfg.Timer.Enabled,
		timeWheelTick:   cfg.Timer.Tick,
		timeWheelSize:   cfg.Timer.WheelSize,
	}, nil
}

// ——— 名称验证 ———

func (b *Builder) validateNames() error {
	seen := make(map[string]string)
	for _, e := range b.services {
		name := e.comp.Name()
		if existing, ok := seen[name]; ok {
			return fmt.Errorf("%w: %s（Service）与 %s 冲突", constants.ErrDuplicateName, name, existing)
		}
		seen[name] = "Service"
	}
	for _, e := range b.modules {
		name := e.comp.Name()
		if existing, ok := seen[name]; ok {
			return fmt.Errorf("%w: %s（Module）与 %s 冲突", constants.ErrDuplicateName, name, existing)
		}
		seen[name] = "Module"
	}
	return nil
}

// ——— Service 顺序解析（拓扑排序）———

func (b *Builder) resolveServices() ([]component.Component, error) {
	if len(b.services) == 0 {
		return nil, nil
	}

	nameToEntry := make(map[string]*serviceEntry, len(b.services))
	for _, e := range b.services {
		nameToEntry[e.comp.Name()] = e
	}

	inDegree := make(map[string]int, len(b.services))
	graph := make(map[string][]string, len(b.services))

	for _, e := range b.services {
		name := e.comp.Name()
		if _, ok := inDegree[name]; !ok {
			inDegree[name] = 0
		}
		for _, target := range e.before {
			if _, ok := nameToEntry[target]; !ok {
				return nil, fmt.Errorf("Service %s 引用了未知的 Service %s（WithServiceBefore）", name, target)
			}
			graph[name] = append(graph[name], target)
			inDegree[target]++
		}
		for _, target := range e.after {
			if _, ok := nameToEntry[target]; !ok {
				return nil, fmt.Errorf("Service %s 引用了未知的 Service %s（WithServiceAfter）", name, target)
			}
			graph[target] = append(graph[target], name)
			inDegree[name]++
		}
	}

	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue)

	var result []component.Component
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		result = append(result, nameToEntry[name].comp)

		var next []string
		for _, dependent := range graph[name] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				next = append(next, dependent)
			}
		}
		sort.Strings(next)
		queue = append(queue, next...)
	}

	if len(result) != len(b.services) {
		return nil, fmt.Errorf("Service 顺序约束中存在循环依赖")
	}

	return result, nil
}

// ——— Module 顺序解析（拓扑排序）———

func (b *Builder) resolveModules() ([]component.Component, error) {
	if len(b.modules) == 0 {
		return nil, nil
	}

	nameToEntry := make(map[string]*moduleEntry, len(b.modules))
	for _, e := range b.modules {
		nameToEntry[e.comp.Name()] = e
	}

	for _, e := range b.modules {
		for _, dep := range e.dependsOn {
			if _, ok := nameToEntry[dep]; !ok {
				return nil, fmt.Errorf("Module %s 依赖了未知的 Module %s", e.comp.Name(), dep)
			}
		}
	}

	inDegree := make(map[string]int, len(b.modules))
	graph := make(map[string][]string, len(b.modules))

	for name := range nameToEntry {
		inDegree[name] = 0
	}

	for _, e := range b.modules {
		for _, dep := range e.dependsOn {
			graph[dep] = append(graph[dep], e.comp.Name())
			inDegree[e.comp.Name()]++
		}
	}

	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue)

	var result []component.Component
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		result = append(result, nameToEntry[name].comp)

		var next []string
		for _, dependent := range graph[name] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				next = append(next, dependent)
			}
		}
		sort.Strings(next)
		queue = append(queue, next...)
	}

	if len(result) != len(b.modules) {
		return nil, fmt.Errorf("Module 依赖中存在循环依赖")
	}

	return result, nil
}
