package group

import (
	"sync"

	"projectbid/server/logger"
)

type groupServiceImpl struct {
	mu     sync.RWMutex
	groups map[string]*Group
}

func newGroupServiceImpl() *groupServiceImpl {
	return &groupServiceImpl{
		groups: make(map[string]*Group),
	}
}

func (gs *groupServiceImpl) NewGroup(name string) *Group {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	if g, ok := gs.groups[name]; ok {
		return g
	}

	g := NewGroup(name)
	gs.groups[name] = g
	logger.Debugw("创建分组", "分组名", name)
	return g
}

func (gs *groupServiceImpl) GetGroup(name string) *Group {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	return gs.groups[name]
}

func (gs *groupServiceImpl) RemoveGroup(name string) bool {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	if g, ok := gs.groups[name]; ok {
		g.Close()
		delete(gs.groups, name)
		logger.Debugw("移除分组", "分组名", name)
		return true
	}
	return false
}

func (gs *groupServiceImpl) Count() int {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	return len(gs.groups)
}
