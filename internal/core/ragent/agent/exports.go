package agent

import "fmt"

// RegisterConn 注册连接（集成测试用）
func (m *Runtime) RegisterConn(nodeID uint32, c *UDSConn) {
	m.registerConn(nodeID, c)
}

// RouteFrame 路由帧（集成测试用）
func (m *Runtime) RouteFrame(c *UDSConn, frame Frame) {
	m.router.RouteFrame(c, frame)
}

// MemberTable 返回成员表
func (m *Runtime) MemberTable() *MemberTable { return m.memberTable }

// PeerMgr 返回 peer 管理器
func (m *Runtime) PeerMgr() *PeerMgr { return m.peerMgr }

// Resolver 返回路由解析器
func (m *Runtime) Resolver() *Resolver { return m.resolver }

// KeepAlive 返回保活器
func (m *Runtime) KeepAlive() *KeepAlive { return m.keepalive }

// PeerForwarder 返回 peer 转发器（集成测试用）
func (m *Runtime) PeerForwarder() *PeerForwarder { return m.peerFwd }

// Router 返回路由器（集成测试用）
func (m *Runtime) Router() *Router { return m.router }

// ListenAddr 公开（集成测试用）
func (m *Runtime) ListenAddr() string { return m.listenAddr }

// SetListenAddr 设置监听地址（集成测试用）
func (m *Runtime) SetListenAddr(addr string) { m.listenAddr = addr }

// RemoteSeqMap 返回 RemoteSeqMap（集成测试用）
func (m *Runtime) RemoteSeqMap() *RemoteSeqMap { return m.remoteSeq }

// DeliverToLocalConn 投递给本地连接（集成测试用）
func (m *Runtime) DeliverToLocalConn(nodeID uint32, f Frame) {
	_ = m.localConns.Deliver(nodeID, f)
}

// DebugString 返回模块状态
func (m *Runtime) DebugString() string {
	return fmt.Sprintf("routeragent(sock=%s)", m.sockPath)
}

// PosterFunc 将 func(func()) 适配为 app.Poster
type PosterFunc func(func())

func (f PosterFunc) Post(fn func()) { f(fn) }

// NewRuntimeForTest 创建用于测试的 runtime
func NewRuntimeForTest(p func(func())) *Runtime {
	m := NewRuntime()
	m.poster = PosterFunc(p)
	m.peerFwd.poster = m.poster
	return m
}
