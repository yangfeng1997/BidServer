// src/servers/lobbysvr/internal/component_friend.go
package internal

const (
	FriendComponentName = "friend"
	FriendField         = "friend"
)

// FriendState 好友存储态（内嵌 players 文档 friend 子文档）
type FriendState struct {
	Friends []int64 `bson:"friends"`
}

func NewFriendState() FriendState { return FriendState{Friends: []int64{}} }

// Friend 好友组件：uid 集合，全本地写。仅主循环用，零锁。
type Friend struct {
	friends map[int64]struct{}
	dirty   bool
}

func NewFriend() *Friend { return &Friend{friends: make(map[int64]struct{})} }

func (f *Friend) Name() string  { return FriendComponentName }
func (f *Friend) Field() string { return FriendField }
func (f *Friend) Dirty() bool   { return f.dirty }
func (f *Friend) ClearDirty()   { f.dirty = false }
func (f *Friend) MarkDirty()    { f.dirty = true }

// Load 从存储态恢复内存态（覆盖，清脏）
func (f *Friend) Load(s *FriendState) {
	f.friends = make(map[int64]struct{}, len(s.Friends))
	for _, u := range s.Friends {
		f.friends[u] = struct{}{}
	}
	f.dirty = false
}

// Snapshot 返回可落库快照（值拷贝）
func (f *Friend) Snapshot() any {
	out := make([]int64, 0, len(f.friends))
	for u := range f.friends {
		out = append(out, u)
	}
	return FriendState{Friends: out}
}

// Has 判断 uid 是否在好友集中
func (f *Friend) Has(uid int64) bool { _, ok := f.friends[uid]; return ok }

// Add 加好友，已存在返回 false（不重复标脏）；新增返回 true 并标脏。
func (f *Friend) Add(uid int64) bool {
	if _, ok := f.friends[uid]; ok {
		return false
	}
	f.friends[uid] = struct{}{}
	f.dirty = true
	return true
}

// Remove 删好友，存在则删并标脏返回 true；否则 false。
func (f *Friend) Remove(uid int64) bool {
	if _, ok := f.friends[uid]; !ok {
		return false
	}
	delete(f.friends, uid)
	f.dirty = true
	return true
}

// List 返回好友 uid 副本
func (f *Friend) List() []int64 {
	out := make([]int64, 0, len(f.friends))
	for u := range f.friends {
		out = append(out, u)
	}
	return out
}

// 编译期断言 Friend 满足 Component
var _ Component = (*Friend)(nil)
