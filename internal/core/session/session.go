package session

import (
	"fmt"
	"sync"
	"sync/atomic"

	"project/internal/core/conn"
)

// 连接上的玩家会话
type Session struct {
	ID         string
	UID        int64
	ConnID     string
	Conn       conn.Connection
	Authed     bool
	BoundNodes map[uint32]uint32
}

// 绑定指定 serverType 的 nodeID
func (s *Session) SetBound(serverType, nodeID uint32) {
	if s.BoundNodes == nil {
		s.BoundNodes = make(map[uint32]uint32)
	}
	s.BoundNodes[serverType] = nodeID
}

// SetAuthed 设置认证状态
func (s *Session) SetAuthed(authed bool) {
	s.Authed = authed
}

// 会话管理器
type SessionManager struct {
	mu     sync.RWMutex
	seq    atomic.Uint64
	byConn map[string]*Session
	byUID  map[int64]*Session
}

// 创建会话管理器
func NewSessionManager() *SessionManager {
	return &SessionManager{
		byConn: make(map[string]*Session),
		byUID:  make(map[int64]*Session),
	}
}

// 创建新会话
func (m *SessionManager) OnConnect(c conn.Connection) *Session {
	if c == nil {
		return nil
	}
	sess := &Session{
		ID:         fmt.Sprintf("%d-%s", m.seq.Add(1), c.RemoteAddr()),
		ConnID:     c.RemoteAddr(),
		Conn:       c,
		BoundNodes: make(map[uint32]uint32),
	}
	m.mu.Lock()
	m.byConn[sess.ConnID] = sess
	m.mu.Unlock()
	return sess
}

// 断开时清理会话
func (m *SessionManager) OnDisconnect(c conn.Connection) {
	if c == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.byConn[c.RemoteAddr()]
	if !ok {
		return
	}
	delete(m.byConn, sess.ConnID)
	if sess.UID != 0 {
		delete(m.byUID, sess.UID)
	}
}

// 超时清理
func (m *SessionManager) OnTimeout(c conn.Connection) { m.OnDisconnect(c) }

// 绑定 UID 和亲和节点
func (m *SessionManager) BindSession(connID string, uid int64, bound map[uint32]uint32) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess := m.byConn[connID]
	if sess == nil {
		sess = &Session{ID: connID, ConnID: connID, UID: uid, BoundNodes: make(map[uint32]uint32)}
		m.byConn[connID] = sess
	}
	// 若 UID 已被其他 session 占用，清除旧 session 的 UID 以保护 byUID 索引
	if old := m.byUID[uid]; old != nil && old != sess {
		old.UID = 0
		old.Authed = false
	}
	if sess.UID != 0 {
		delete(m.byUID, sess.UID)
	}
	sess.UID = uid
	sess.Authed = true
	if sess.BoundNodes == nil {
		sess.BoundNodes = make(map[uint32]uint32)
	}
	for k, v := range bound {
		sess.BoundNodes[k] = v
	}
	// 只有 uid 不为 0 才写入 byUID 索引
	if uid != 0 {
		m.byUID[uid] = sess
	}
	return sess
}

// 写入单个亲和节点
func (m *SessionManager) SetBound(uid int64, serverType, nodeID uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess := m.byUID[uid]; sess != nil {
		sess.SetBound(serverType, nodeID)
	}
}

// 按连接 ID 查找会话
func (m *SessionManager) GetByConnID(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byConn[id]
}

// 按 UID 查找会话
func (m *SessionManager) GetByUID(uid int64) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byUID[uid]
}
