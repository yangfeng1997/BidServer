package internal

import (
	"context"
	lobbypb "project/protocal/gen/lobby"
	"project/src/common/logger"
	"project/src/common/taskqueue"
	"project/src/framework/agent"
	"project/src/framework/cluster"
	"project/src/framework/module"
	"project/src/framework/session"
)

// GateModule gate 服务模块，管理客户端连接的业务生命周期
type GateModule struct {
	module.BaseModule
	nodeID   string
	agents   *agent.Map
	sessions *session.Manager
	cls      cluster.Cluster
	tq       *taskqueue.Queue
	ctx      context.Context
}

func NewGateModule(nodeID string, sessions *session.Manager, cls cluster.Cluster, agents *agent.Map) *GateModule {
	return &GateModule{nodeID: nodeID, sessions: sessions, cls: cls, agents: agents}
}

// NodeID 本 gateway 节点 ID（点分），登录转发时填入 ClusterSession.FrontendId
func (g *GateModule) NodeID() string { return g.nodeID }

// Agents 返回 sessionID → Agent 索引，供顶号推送
func (g *GateModule) Agents() *agent.Map { return g.agents }

func (g *GateModule) Name() string { return "gate" }

func (g *GateModule) Init() {
	g.tq = taskqueue.New(512)
	g.ctx = cluster.WithCluster(context.Background(), g.cls)
	g.ctx = cluster.WithDispatch(g.ctx, g.tq)
	logger.Info("gate module initialized")
}

func (g *GateModule) OnAfterInit() {
	// 注册 session 关闭回调：玩家断线时通知后端下线
	g.sessions.OnClose(func(s *session.Session) {
		if s.IsBound() {
			logger.Info("player disconnected",
				logger.Int64("uid", s.UID()),
				logger.String("ip", s.IP()))
			g.notifyPlayerOffline(s)
		}
	})
	logger.Info("gate module ready")
}

func (g *GateModule) OnBeforeStop() {
	logger.Info("gate stopping, kicking all players")
}

func (g *GateModule) OnStop() {
	logger.Info("gate stopped")
}

// Ctx 返回注入了 cluster 和 dispatcher 的 ctx，供 handler 使用
func (g *GateModule) Ctx() context.Context { return g.ctx }

// Sessions 返回 session 管理器
func (g *GateModule) Sessions() *session.Manager { return g.sessions }

// Cluster 返回集群实例
func (g *GateModule) Cluster() cluster.Cluster { return g.cls }

func (g *GateModule) notifyPlayerOffline(s *session.Session) {
	lobbyNode, ok := s.BoundNode("lobbysvr")
	if !ok {
		return
	}
	target, err := cluster.ParseNodeID(lobbyNode)
	if err != nil {
		logger.Warn("gate offline: bad lobby nodeID", logger.String("nodeID", lobbyNode))
		return
	}
	if err := g.cls.Cast(g.ctx, target, "LobbyHandler.playerdisconnect",
		&lobbypb.RPC_PlayerDisconnect_Notify{Uid: s.UID()}); err != nil {
		logger.Warn("gate offline: cast failed", logger.Int64("uid", s.UID()), logger.Err(err))
	}
}

// ForwardTouch 把活跃信号转发给绑定 lobby（lobby 侧节流 + Touch onlinesvr）
func (g *GateModule) ForwardTouch(s *session.Session) {
	lobbyNode, ok := s.BoundNode("lobbysvr")
	if !ok {
		return
	}
	target, err := cluster.ParseNodeID(lobbyNode)
	if err != nil {
		logger.Warn("gate touch: bad lobby nodeID", logger.String("nodeID", lobbyNode))
		return
	}
	if err := g.cls.Cast(g.ctx, target, "LobbyHandler.touch",
		&lobbypb.RPC_Touch_Notify{Uid: s.UID()}); err != nil {
		logger.Warn("gate touch: cast failed", logger.Int64("uid", s.UID()), logger.Err(err))
	}
}
