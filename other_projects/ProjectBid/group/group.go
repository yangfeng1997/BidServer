// Package group 提供游戏内分组管理，支持成员增删、组内广播与过滤推送。
package group

import (
	"errors"
	"sync"
	"sync/atomic"

	"projectbid/server/logger"
	"projectbid/server/session"
)

const (
	statusWorking = 0
	statusClosed  = 1
)

// 分组错误。
var (
	ErrClosedGroup      = errors.New("分组已关闭")
	ErrMemberNotFound   = errors.New("成员未找到")
	ErrMemberDuplicated = errors.New("成员已存在")
)

// SessionFilter 用于过滤 Multicast 的目标会话。
type SessionFilter func(session.Session) bool

// Group 表示一个会话组，消息发送给组内所有成员。
type Group struct {
	mu       sync.RWMutex
	status   int32
	name     string
	sessions map[int64]session.Session
}

// NewGroup 创建新分组。
func NewGroup(name string) *Group {
	return &Group{
		status:   statusWorking,
		name:     name,
		sessions: make(map[int64]session.Session),
	}
}

// Name 返回分组名称。
func (g *Group) Name() string { return g.name }

// Add 将会话加入分组。
func (g *Group) Add(s session.Session) error {
	if g.isClosed() {
		return ErrClosedGroup
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	id := s.ID()
	if _, ok := g.sessions[id]; ok {
		return ErrMemberDuplicated
	}

	g.sessions[id] = s
	logger.Debugw("会话加入分组",
		"分组名", g.name,
		"会话ID", s.ID(),
		"用户", s.UID(),
	)
	return nil
}

// Remove 从分组中移除会话。
func (g *Group) Remove(s session.Session) error {
	if g.isClosed() {
		return ErrClosedGroup
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	delete(g.sessions, s.ID())
	logger.Debugw("会话离开分组",
		"分组名", g.name,
		"会话ID", s.ID(),
		"用户", s.UID(),
	)
	return nil
}

// Broadcast 向组内所有成员推送消息。
func (g *Group) Broadcast(route string, v interface{}) error {
	if g.isClosed() {
		return ErrClosedGroup
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	var lastErr error
	for _, s := range g.sessions {
		if err := s.Push(route, v); err != nil {
			logger.Debugw("分组广播推送失败",
				"分组名", g.name,
				"会话ID", s.ID(),
				"用户", s.UID(),
				"错误", err,
			)
			lastErr = err
		}
	}
	return lastErr
}

// BroadcastExcept 向组内除指定会话外的所有成员推送消息。
func (g *Group) BroadcastExcept(route string, v interface{}, exceptSessionID int64) error {
	if g.isClosed() {
		return ErrClosedGroup
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	var lastErr error
	for _, s := range g.sessions {
		if s.ID() == exceptSessionID {
			continue
		}
		if err := s.Push(route, v); err != nil {
			logger.Debugw("分组广播推送失败",
				"分组名", g.name,
				"会话ID", s.ID(),
				"错误", err,
			)
			lastErr = err
		}
	}
	return lastErr
}

// Multicast 向满足 filter 条件的成员推送消息。
func (g *Group) Multicast(route string, v interface{}, filter SessionFilter) error {
	if g.isClosed() {
		return ErrClosedGroup
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	var lastErr error
	for _, s := range g.sessions {
		if filter != nil && !filter(s) {
			continue
		}
		if err := s.Push(route, v); err != nil {
			logger.Debugw("分组组播推送失败",
				"分组名", g.name,
				"会话ID", s.ID(),
				"错误", err,
			)
			lastErr = err
		}
	}
	return lastErr
}

// Members 返回组内所有成员的 UID 列表。
func (g *Group) Members() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	members := make([]string, 0, len(g.sessions))
	for _, s := range g.sessions {
		members = append(members, s.UID())
	}
	return members
}

// Count 返回成员数量。
func (g *Group) Count() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.sessions)
}

// Contains 检查指定会话是否在组内。
func (g *Group) Contains(sessionID int64) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.sessions[sessionID]
	return ok
}

// Clear 清空所有成员。
func (g *Group) Clear() error {
	if g.isClosed() {
		return ErrClosedGroup
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.sessions = make(map[int64]session.Session)
	return nil
}

// Close 关闭分组，释放所有成员引用。
func (g *Group) Close() error {
	if g.isClosed() {
		return ErrClosedGroup
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	atomic.StoreInt32(&g.status, statusClosed)
	g.sessions = make(map[int64]session.Session)
	return nil
}

func (g *Group) isClosed() bool {
	return atomic.LoadInt32(&g.status) == statusClosed
}
