package session

import (
	"context"
	"errors"
	"project/src/common/syncmap"
	"sync"
	"sync/atomic"
)

// ErrAlreadyBound Session 已绑定 UID 时返回
var ErrAlreadyBound = errors.New("session already bound to a uid")

// BindCallback Bind 前置回调，返回 error 可阻止绑定
type BindCallback func(ctx context.Context, s *Session) error

// CloseCallback Session 关闭全局回调
type CloseCallback func(s *Session)

// Manager 全局 Session 管理，维护 ID 和 UID 双索引
type Manager struct {
	byID      syncmap.Map[int64, *Session]
	byUID     syncmap.Map[int64, *Session]
	closeOnce syncmap.Map[int64, *sync.Once]
	count     atomic.Int64
	idGen     atomic.Int64

	onBind    []BindCallback
	afterBind []BindCallback
	onClose   []CloseCallback
}

func NewManager() *Manager { return &Manager{} }

// OnBind 注册 Bind 前置回调，失败时自动回滚
func (m *Manager) OnBind(fn BindCallback) { m.onBind = append(m.onBind, fn) }

// AfterBind 注册 Bind 成功后回调
func (m *Manager) AfterBind(fn BindCallback) { m.afterBind = append(m.afterBind, fn) }

// OnClose 注册 Session 关闭全局回调
func (m *Manager) OnClose(fn CloseCallback) { m.onClose = append(m.onClose, fn) }

// New 创建 Session 并注册到 Manager
func (m *Manager) New(ip string) *Session {
	id := m.idGen.Add(1)
	s := newSession(id, ip)
	m.byID.Store(id, s)
	m.count.Add(1)
	return s
}

// ByID 按 ID 查找 Session
func (m *Manager) ByID(id int64) (*Session, bool) {
	return m.byID.Load(id)
}

// ByUID 按 UID 查找 Session
func (m *Manager) ByUID(uid int64) (*Session, bool) {
	return m.byUID.Load(uid)
}

// Count 返回当前在线 Session 数量，O(1)
func (m *Manager) Count() int64 { return m.count.Load() }

// Bind 将 Session 与业务 UID 绑定，自动踢出同 UID 旧连接
func (m *Manager) Bind(ctx context.Context, s *Session, uid int64) error {
	s.mu.Lock()
	if s.uid != 0 {
		s.mu.Unlock()
		return ErrAlreadyBound
	}
	s.uid = uid
	s.mu.Unlock()

	for _, cb := range m.onBind {
		if err := cb(ctx, s); err != nil {
			s.mu.Lock()
			s.uid = 0
			s.mu.Unlock()
			return err
		}
	}

	// 踢出同 UID 旧 Session
	if old, loaded := m.byUID.Swap(uid, s); loaded {
		m.Close(old)
	}

	for _, cb := range m.afterBind {
		_ = cb(ctx, s)
	}
	return nil
}

// Close 关闭 Session，移除双索引，触发全局回调（幂等）
func (m *Manager) Close(s *Session) {
	once, _ := m.closeOnce.LoadOrStore(s.id, &sync.Once{})
	once.Do(func() {
		m.closeOnce.Delete(s.id)
		m.byID.Delete(s.id)
		m.count.Add(-1)

		// ID 校验防止新旧连接混淆
		if val, ok := m.byUID.Load(s.UID()); ok && val.id == s.id {
			m.byUID.Delete(s.UID())
		}

		for _, cb := range m.onClose {
			cb(s)
		}
	})
}

// CloseAll 关闭所有 Session
func (m *Manager) CloseAll() {
	m.byID.Range(func(_ int64, s *Session) bool {
		m.Close(s)
		return true
	})
}
