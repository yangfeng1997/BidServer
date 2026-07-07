package internal

import (
	"strconv"

	"project/src/common/logger"
)

const (
	BagComponentName = "bag"
	BagField         = "bag" // players 文档中的 bson 字段
	maxRecentOps     = 128   // op-id 去重环大小
)

// BagState 背包的存储态（内嵌 players 文档；bson map 键须为 string）
type BagState struct {
	Items map[string]int32 `bson:"items"`         // itemID(字符串) → 数量
	Ops   []string         `bson:"ops,omitempty"` // 持久化 op-id 去重环
}

func NewBagState() BagState { return BagState{Items: map[string]int32{}} }

// Bag 背包组件：内存态 itemID(int32) → 数量；op-id 去重保幂等
type Bag struct {
	items map[int32]int32
	ops   *opDedup
	dirty bool
}

func NewBag() *Bag {
	return &Bag{items: make(map[int32]int32), ops: newOpDedup(maxRecentOps)}
}

func (b *Bag) Name() string  { return BagComponentName }
func (b *Bag) Field() string { return BagField }
func (b *Bag) Dirty() bool   { return b.dirty }
func (b *Bag) ClearDirty()   { b.dirty = false }
func (b *Bag) MarkDirty()    { b.dirty = true }

// Load 从存储态恢复内存态（覆盖，清脏）；非法/越界的 item id 键记日志并跳过（防脏数据）
func (b *Bag) Load(s *BagState) {
	b.items = make(map[int32]int32, len(s.Items))
	for k, v := range s.Items {
		id, err := strconv.ParseInt(k, 10, 32)
		if err != nil {
			logger.Warn("背包加载：跳过非法 item id", logger.String("key", k), logger.Err(err))
			continue
		}
		b.items[int32(id)] = v
	}
	b.ops.loadFrom(s.Ops)
	b.dirty = false
}

// Snapshot 返回可落库快照（值拷贝）
func (b *Bag) Snapshot() any {
	items := make(map[string]int32, len(b.items))
	for id, c := range b.items {
		items[strconv.Itoa(int(id))] = c
	}
	return BagState{Items: items, Ops: b.ops.snapshot()}
}

// Count 返回某道具数量
func (b *Bag) Count(itemID int32) int32 { return b.items[itemID] }

// Items 返回内存态副本（避免外部改内部）
func (b *Bag) Items() map[int32]int32 {
	out := make(map[int32]int32, len(b.items))
	for k, v := range b.items {
		out[k] = v
	}
	return out
}

// Add 幂等加道具：opID 非空且已见过则跳过（返回当前数量）；否则按 count 增减并标脏。
// count 为负表示扣减；归零或转负的道具从背包移除。
func (b *Bag) Add(opID string, itemID, count int32) int32 {
	if b.ops.seen(opID) {
		return b.items[itemID]
	}
	if count != 0 {
		b.items[itemID] += count
		if b.items[itemID] <= 0 {
			delete(b.items, itemID)
		}
		b.dirty = true
	}
	b.ops.remember(opID)
	return b.items[itemID]
}

// ProtectOp 标记 opID 免于 op-dedup 环淘汰（滞留重放源未消费前）。
func (b *Bag) ProtectOp(opID string) { b.ops.protect(opID) }

// UnprotectOp 解除保护（重放源已 $pull 成功后）。
func (b *Bag) UnprotectOp(opID string) { b.ops.unprotect(opID) }

// 编译期断言 Bag 满足 Component
var _ Component = (*Bag)(nil)
