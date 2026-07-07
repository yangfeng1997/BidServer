package session

import "sync"

// Session 客户端连接的业务数据载体，纯数据对象。
// 生命周期管理（关闭回调、请求 draining）由 Agent 负责。
type Session struct {
	mu          sync.RWMutex
	id          int64
	uid         int64  // 0 表示未绑定
	data        map[string]any
	encodedData []byte // data 的序列化缓存，data 变更时置 nil
	ip          string
	frontendID  string
	boundNodes  map[string]string // serverTypeName → nodeID 点分格式，如 "roomsvr" → "1.7.1"
}

func newSession(id int64, ip string) *Session {
	return &Session{
		id:         id,
		ip:         ip,
		data:       make(map[string]any),
		boundNodes: make(map[string]string),
	}
}

func (s *Session) ID() int64          { return s.id }
func (s *Session) IP() string         { return s.ip }
func (s *Session) FrontendID() string { return s.frontendID }

func (s *Session) SetFrontendID(id string) {
	s.mu.Lock()
	s.frontendID = id
	s.mu.Unlock()
}

func (s *Session) UID() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.uid
}

// IsBound 返回是否已绑定 UID
func (s *Session) IsBound() bool { return s.UID() != 0 }

// BindNode 绑定玩家到指定类型的特定节点，gate 转发时自动路由到该节点
// 典型用法：玩家进入对局后，gate 记录 "roomsvr" → "1.7.1"
func (s *Session) BindNode(serverTypeName string, nodeID string) {
	s.mu.Lock()
	s.boundNodes[serverTypeName] = nodeID
	s.mu.Unlock()
}

// UnbindNode 解绑指定类型的节点绑定
func (s *Session) UnbindNode(serverTypeName string) {
	s.mu.Lock()
	delete(s.boundNodes, serverTypeName)
	s.mu.Unlock()
}

// BoundNode 返回玩家在指定服务类型上绑定的节点 ID，未绑定时返回 ("", false)
func (s *Session) BoundNode(serverTypeName string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nodeID, ok := s.boundNodes[serverTypeName]
	return nodeID, ok
}

// Set 设置业务 KV，同时清除序列化缓存
func (s *Session) Set(key string, val any) {
	s.mu.Lock()
	s.data[key] = val
	s.encodedData = nil
	s.mu.Unlock()
}

// Get 读取业务 KV
func (s *Session) Get(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// GetTyped 泛型版 Get，直接返回具体类型，消除调用方类型断言
//
//	score, ok := session.GetTyped[int64](s, "score")
func GetTyped[T any](s *Session, key string) (T, bool) {
	v, ok := s.Get(key)
	if !ok {
		var zero T
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// Delete 删除业务 KV
func (s *Session) Delete(key string) {
	s.mu.Lock()
	delete(s.data, key)
	s.encodedData = nil
	s.mu.Unlock()
}
