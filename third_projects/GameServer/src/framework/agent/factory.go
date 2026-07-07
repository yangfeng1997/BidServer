package agent

import (
	"context"
	"project/src/framework/handler"
	"project/src/framework/network/acceptor"
	"project/src/framework/network/handshake"
	"project/src/framework/session"
	"sync"
	"sync/atomic"
)

// Factory 负责为每条新连接创建 connAgent
type Factory struct {
	registry       *handler.Registry
	sessions       *session.Manager
	wg             *sync.WaitGroup
	nextID         atomic.Uint64
	validators     []handshake.Validator
	heartbeatSec   int
	serializerName string

	// 路由表，由 Application 注入
	msgRouteTable  map[uint32]string
	forwardTable   map[uint32]string
	respMsgIDTable map[uint32]uint32
	forwardFn      func(ctx context.Context, fctx *ForwardContext)

	// AgentMap：自动维护 sessionID → Agent 索引，供 backend Push 使用
	agentMap *Map
}

func NewFactory(registry *handler.Registry, sessions *session.Manager, wg *sync.WaitGroup, heartbeatSec int, serializerName string) *Factory {
	return &Factory{
		registry:       registry,
		sessions:       sessions,
		wg:             wg,
		heartbeatSec:   heartbeatSec,
		serializerName: serializerName,
		agentMap:       NewMap(),
	}
}

// AgentMap 返回 sessionID → Agent 索引，供 gate 的 Push 路径使用
func (f *Factory) AgentMap() *Map { return f.agentMap }

// SetRouteTables 注入路由表（由 Application 在 Start 前调用）
func (f *Factory) SetRouteTables(
	msgRoute map[uint32]string,
	forward map[uint32]string,
	respMsgID map[uint32]uint32,
	forwardFn func(ctx context.Context, fctx *ForwardContext),
) {
	f.msgRouteTable = msgRoute
	f.forwardTable = forward
	f.respMsgIDTable = respMsgID
	if forwardFn != nil {
		f.forwardFn = forwardFn
	}
}

// SetForwardFunc 注入转发函数，gate 服务在初始化时调用
// 转发函数负责把消息路由到对应的 backend 服务（通过 cluster.Call/Cast）
func (f *Factory) SetForwardFunc(fn func(ctx context.Context, fctx *ForwardContext)) {
	f.forwardFn = fn
}

// AddHandshakeValidator 注册握手校验函数，按注册顺序执行
func (f *Factory) AddHandshakeValidator(v handshake.Validator) {
	f.validators = append(f.validators, v)
}

// NewAgent 创建 agent 并绑定 session，同时预占 wg 计数（Handle 退出时 Done）
func (f *Factory) NewAgent(conn acceptor.ClientConn) *connAgent {
	s := f.sessions.New(conn.RemoteAddr().String())
	f.wg.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	ag := &connAgent{
		conn:           conn,
		session:        s,
		sessions:       f.sessions,
		chSend:         make(chan []byte, sendChanSize),
		chDie:          make(chan struct{}),
		ctx:            ctx,
		cancel:         cancel,
		wg:             f.wg,
		registry:       f.registry,
		validators:     f.validators,
		heartbeatSec:   f.heartbeatSec,
		serializerName: f.serializerName,
		msgRouteTable:  f.msgRouteTable,
		forwardTable:   f.forwardTable,
		respMsgIDTable: f.respMsgIDTable,
		forwardFn:      f.forwardFn,
	}
	// 自动维护 AgentMap：连接建立时存入，关闭时删除
	f.agentMap.Store(s.ID(), ag)
	ag.OnClose(func(sess *session.Session) {
		f.agentMap.Delete(sess.ID())
	})
	return ag
}
