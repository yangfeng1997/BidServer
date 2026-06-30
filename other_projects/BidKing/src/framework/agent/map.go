package agent

import (
	"project/src/common/syncmap"
)

// Map 维护 sessionID → Agent 的索引，供 gate 的 Push 路径使用。
// backend 通过 cluster 发送 Push 消息到 gate，gate 按 sessionID 找到 Agent 推给客户端。
//
// 典型用法：
//
//	// gate 启动时创建
//	agentMap := agent.NewMap()
//
//	// 注册到 session 关闭回调，自动清理
//	sessions.OnClose(func(s *session.Session) {
//	    agentMap.Delete(s.ID())
//	})
//
//	// 连接建立时存入（在 factory 或 OnAfterInit 里）
//	agentMap.Store(ag.Session().ID(), ag)
//
//	// backend Push 时取出
//	if ag, ok := agentMap.Load(sessionID); ok {
//	    ag.Push(msgID, data)
//	}
type Map struct {
	m syncmap.Map[int64, Agent]
}

func NewMap() *Map { return &Map{} }

func (m *Map) Store(sessionID int64, ag Agent)    { m.m.Store(sessionID, ag) }
func (m *Map) Load(sessionID int64) (Agent, bool) { return m.m.Load(sessionID) }
func (m *Map) Delete(sessionID int64)             { m.m.Delete(sessionID) }

// Range 遍历所有连接，f 返回 false 时停止。委托底层 syncmap.Map.Range
// （sync.Map.Range 语义：允许遍历中删除当前 key，停机逐连接 Close 触发的
// agentMap.Delete 因此安全）。
func (m *Map) Range(f func(int64, Agent) bool) { m.m.Range(f) }
