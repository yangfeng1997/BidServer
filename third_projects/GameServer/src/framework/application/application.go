package application

import (
	"context"
	"os"
	"os/signal"
	"project/src/common/logger"
	"project/src/common/serialize"
	"project/src/framework/agent"
	"project/src/framework/cluster"
	clusterpb "project/src/framework/cluster/pb"
	"project/src/framework/handler"
	"project/src/framework/module"
	"project/src/framework/network/acceptor"
	"project/src/framework/network/handshake"
	"project/src/framework/network/message"
	"project/src/framework/pipeline"
	"project/src/framework/session"
	"runtime/debug"
	"sync"
	"syscall"
	"time"
)

const (
	shutdownTimeout     = 10 * time.Second
	defaultHeartbeatSec = 30
)

// Application 应用进程框架的核心类型：编排模块生命周期、网络接入、集群通信。
type Application struct {
	nodeID     string
	nodeType   string
	isFrontend bool

	modules   []module.Module
	acceptors []acceptor.Acceptor
	registry  *handler.Registry
	agentFac  *agent.Factory
	cls       cluster.Cluster
	sessions  *session.Manager

	heartbeatSec    int
	serializerName  string
	shutdownTimeout time.Duration

	// 路由表
	msgRouteTable   map[uint32]string
	forwardTable    map[uint32]string
	respMsgIDTable  map[uint32]uint32
	customForwardFn func(ctx context.Context, fctx *agent.ForwardContext)

	agentWg sync.WaitGroup
	running bool
	dieChan chan struct{}
	once    sync.Once
}

func New(opts ...Option) *Application {
	a := &Application{
		nodeID:          "node-1",
		nodeType:        "default",
		cls:             cluster.NewNoopCluster(),
		sessions:        session.NewManager(),
		dieChan:         make(chan struct{}),
		heartbeatSec:    defaultHeartbeatSec,
		shutdownTimeout: shutdownTimeout,
	}
	for _, opt := range opts {
		opt(a)
	}
	if a.registry == nil {
		panic("serializer is required, use WithSerializer()")
	}
	a.agentFac = agent.NewFactory(a.registry, a.sessions, &a.agentWg, a.heartbeatSec, a.serializerName)
	return a
}

func (a *Application) NodeID() string             { return a.nodeID }
func (a *Application) NodeType() string           { return a.nodeType }
func (a *Application) Cluster() cluster.Cluster   { return a.cls }
func (a *Application) Sessions() *session.Manager { return a.sessions }
func (a *Application) AgentMap() *agent.Map       { return a.agentFac.AgentMap() }
func (a *Application) IsRunning() bool            { return a.running }
func (a *Application) DieChan() <-chan struct{}   { return a.dieChan }

func (a *Application) Register(modules ...module.Module) {
	for _, m := range modules {
		if _, exists := a.Find(m.Name()); exists {
			panic("module already registered: " + m.Name())
		}
		a.modules = append(a.modules, m)
	}
}

func (a *Application) Find(name string) (module.Module, bool) {
	for _, m := range a.modules {
		if m.Name() == name {
			return m, true
		}
	}
	return nil, false
}

func (a *Application) AddAcceptor(acc acceptor.Acceptor) {
	if !a.isFrontend {
		panic("AddAcceptor requires WithFrontend() option: backend nodes cannot accept client connections")
	}
	a.acceptors = append(a.acceptors, acc)
}

// RegisterHandler 反射扫描 handler 对象的所有合法 handler 方法并注册
// route 格式为 "TypeName.methodname"
// nameFunc 为 nil 时默认小写
func (a *Application) RegisterHandler(handler any, nameFunc func(string) string) error {
	return a.registry.RegisterHandler(handler, nameFunc)
}

func (a *Application) UseBefore(fns ...pipeline.BeforeFunc) {
	a.registry.UseBefore(fns...)
}

func (a *Application) UseAfter(fns ...pipeline.AfterFunc) {
	a.registry.UseAfter(fns...)
}

// AddHandshakeValidator 注册握手校验函数，按注册顺序执行，任一失败则拒绝握手
func (a *Application) AddHandshakeValidator(v handshake.Validator) {
	a.agentFac.AddHandshakeValidator(v)
}

// SetRouteTables 注入路由表，由业务层在 Start() 前调用
// 通常直接传入 routes.MsgRouteTable / routes.ForwardTable / routes.RespMsgIDTable
func (a *Application) SetRouteTables(msgRoute map[uint32]string, forward map[uint32]string, respMsgID map[uint32]uint32) {
	a.msgRouteTable = msgRoute
	a.forwardTable = forward
	a.respMsgIDTable = respMsgID
}

// SetForwardFunc 自定义 gate 转发函数，覆盖框架默认实现
// 默认实现：Request → CallRaw（等返回后 Response 给客户端），OneWay → CastRaw
// 若需要按 uid 路由到固定节点等业务逻辑，通过此方法注入
func (a *Application) SetForwardFunc(fn func(ctx context.Context, fctx *agent.ForwardContext)) {
	a.customForwardFn = fn
}

// withSerializer 内部设置序列化器，由 WithSerializer Option 调用
func (a *Application) withSerializer(ser serialize.Serializer) {
	a.registry = handler.NewRegistry(ser)
}

// Start 非阻塞：初始化所有模块，启动所有 acceptor
func (a *Application) Start() {
	// 集群 handler 注入（前后端都需要，用于接收其他节点的 RPC）
	if hs, ok := a.cls.(cluster.HandlerSetter); ok {
		hs.SetHandler(func(ctx context.Context, data []byte, route string) ([]byte, error) {
			return a.registry.DispatchCluster(ctx, route, data)
		})
	}

	// 路由表和 Acceptor 只对前端节点有意义
	if a.isFrontend {
		var forwardFn func(ctx context.Context, fctx *agent.ForwardContext)
		if a.customForwardFn != nil {
			forwardFn = a.customForwardFn
		} else if len(a.forwardTable) > 0 {
			forwardFn = a.buildDefaultForwardFn()
		}
		a.agentFac.SetRouteTables(a.msgRouteTable, a.forwardTable, a.respMsgIDTable, forwardFn)

		for _, acc := range a.acceptors {
			go func() {
				for conn := range acc.ConnChan() {
					ag := a.agentFac.NewAgent(conn)
					go ag.Handle()
				}
			}()
			go acc.ListenAndServe()
		}
	}

	for _, m := range a.modules {
		m.Init()
	}
	for _, m := range a.modules {
		m.OnAfterInit()
	}
	a.running = true
	logger.Info("application started",
		logger.String("nodeID", a.nodeID),
		logger.String("nodeType", a.nodeType),
		logger.Bool("frontend", a.isFrontend))
}

// newForwardSession 构造转发用 ClusterSession：保留已有字段，填 client_mid/msg_type/frontend_id/uid。
// 抽成纯函数便于单测。
func newForwardSession(base *clusterpb.ClusterSession, mid uint32, msgType uint8, frontendID string, uid int64) *clusterpb.ClusterSession {
	sess := base
	if sess == nil {
		sess = &clusterpb.ClusterSession{}
	}
	sess.ClientMid = mid
	sess.MsgType = uint32(msgType)
	if sess.FrontendId == "" {
		sess.FrontendId = frontendID
	}
	sess.Uid = uid
	return sess
}

// buildDefaultForwardFn 构建内置默认转发函数：
// - 优先查 session 中该 serverType 的节点绑定（有状态服务，如 roomsvr）
// - 无绑定则随机选节点（无状态服务，如 routersvr）
// - Request → CallRaw/CallAnyRaw（等返回后 Response 给客户端）
// - OneWay  → CastRaw/CastAnyRaw（不等返回）
func (a *Application) buildDefaultForwardFn() func(ctx context.Context, fctx *agent.ForwardContext) {
	cls := a.cls
	nodeID := a.nodeID
	return func(ctx context.Context, fctx *agent.ForwardContext) {
		sess := newForwardSession(
			cluster.SessionFromCtx(ctx),
			uint32(fctx.MID), fctx.MsgType, nodeID,
			fctx.Agent.Session().UID(),
		)
		ctx = cluster.WithSession(ctx, sess)

		// 从 msgRouteTable 取 backend handler route
		route, ok := a.msgRouteTable[fctx.MsgID]
		if !ok {
			// proto 里有 server_type 但没有 handler_method 时的 fallback
			route = fctx.ServerType + "Handler.handle"
		}

		// 查 session 中该 serverType 的节点绑定，决定发给哪个节点
		agentSess := fctx.Agent.Session()
		boundNodeID, hasBound := agentSess.BoundNode(fctx.ServerType)

		if message.Type(fctx.MsgType) == message.Request {
			mid := uint32(fctx.MID)
			respMsgID := fctx.RespMsgID
			ag := fctx.Agent
			done := func(respData []byte, err error) {
				if err != nil {
					logger.Warn("forward call failed",
						logger.String("route", route),
						logger.Err(err))
					return
				}
				ag.Response(mid, respMsgID, respData)
			}
			if hasBound {
				// 有状态服务：发到绑定节点
				targetID, err := cluster.ParseNodeID(boundNodeID)
				if err != nil {
					logger.Warn("forward: invalid bound nodeID",
						logger.String("nodeID", boundNodeID), logger.Err(err))
					return
				}
				cls.CallRaw(ctx, targetID, route, fctx.Data, done)
			} else {
				// 无绑定：随机选（无状态服务）
				cls.CallAnyRaw(ctx, fctx.ServerType, route, fctx.Data, done)
			}
		} else {
			// OneWay：不等返回
			var err error
			if hasBound {
				targetID, parseErr := cluster.ParseNodeID(boundNodeID)
				if parseErr != nil {
					logger.Warn("forward: invalid bound nodeID",
						logger.String("nodeID", boundNodeID), logger.Err(parseErr))
					return
				}
				err = cls.CastRaw(ctx, targetID, route, fctx.Data)
			} else {
				err = cls.CastAnyRaw(ctx, fctx.ServerType, route, fctx.Data)
			}
			if err != nil {
				logger.Warn("forward cast failed",
					logger.String("route", route), logger.Err(err))
			}
		}
	}
}

// clusterDieChan 返回集群的 die 信号 channel（集群实现暴露该能力时），否则返回 nil。
// 采用与 Start() 中 a.cls.(cluster.HandlerSetter) 相同的「可选能力」断言风格——
// DieChan 不进 cluster.Cluster 接口，noopCluster / 测试 fake 无需实现。
// nil channel 在 select 中永久阻塞，对无该能力的实现安全。
func (a *Application) clusterDieChan() <-chan struct{} {
	if dc, ok := a.cls.(interface{ DieChan() <-chan struct{} }); ok {
		return dc.DieChan()
	}
	return nil
}

// awaitDie 阻塞直到收到停机触发：OS 信号 / 自身 dieChan / 集群 die 信号。
// 抽成独立方法便于单测（无需安装信号处理器）。
func (a *Application) awaitDie(sigChan <-chan os.Signal) {
	select {
	case sig := <-sigChan:
		logger.Info("received signal", logger.String("signal", sig.String()))
	case <-a.dieChan:
	case <-a.clusterDieChan():
		logger.Warn("cluster signaled die, shutting down")
	}
}

// Run 阻塞等待 SIGINT/SIGTERM 或集群 die 信号，然后执行优雅关闭
func (a *Application) Run() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	a.awaitDie(sigChan)
	a.Stop()
}

// Stop 优雅关闭：通知模块 → 停止 acceptor → 等待 agent → 模块 OnStop
func (a *Application) Stop() {
	a.once.Do(func() {
		a.running = false
		for i := len(a.modules) - 1; i >= 0; i-- {
			m := a.modules[i]
			safeModuleCall(m.Name(), "OnBeforeStop", m.OnBeforeStop)
		}
		for _, acc := range a.acceptors {
			acc.Stop()
		}
		// 主动关闭所有活动连接：TCP acceptor 的 Stop 只关 listener、不关已建连接，
		// 不主动关则读循环挂在 ReadPacket 上直到 shutdownTimeout。Close 幂等（once），
		// 对已被 WS srv.Close() 关掉的连接是 no-op；Close 同时取消每连接 ctx，
		// 中止在途转发。OnClose 回调（agentMap.Delete / notifyPlayerOffline 的 Cast）
		// 不阻塞，故此遍历迅速。
		if a.agentFac != nil {
			a.agentFac.AgentMap().Range(func(_ int64, ag agent.Agent) bool {
				_ = ag.Close()
				return true
			})
		}
		done := make(chan struct{})
		go func() {
			a.agentWg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(a.shutdownTimeout):
			logger.Warn("shutdown timeout, forcing exit")
		}
		for i := len(a.modules) - 1; i >= 0; i-- {
			m := a.modules[i]
			safeModuleCall(m.Name(), "OnStop", m.OnStop)
		}
		logger.Info("shutdown complete")
		close(a.dieChan)
	})
}

// safeModuleCall 执行模块停机回调并隔离 panic：单个模块崩溃不应中断
// 其余模块的逆序停机流程（否则会导致资源泄漏、连接未正确关闭）。
func safeModuleCall(name, phase string, fn func()) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Error("module lifecycle panic",
				logger.String("module", name),
				logger.String("phase", phase),
				logger.String("stack", string(debug.Stack())))
		}
	}()
	fn()
}
