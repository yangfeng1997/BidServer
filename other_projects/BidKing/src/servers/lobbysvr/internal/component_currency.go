// src/servers/lobbysvr/internal/component_currency.go
package internal

const (
	CurrencyComponentName = "currency"
	CurrencyField         = "currency"
	maxCurrencyOps        = 128
)

// CurrencyState 货币存储态（内嵌 players 文档 currency 子文档）
type CurrencyState struct {
	Balances map[string]int64 `bson:"balances"`
	Ops      []string         `bson:"ops,omitempty"` // 持久化 op-id 去重环（跨会话/重投/重放幂等）
}

func NewCurrencyState() CurrencyState { return CurrencyState{Balances: map[string]int64{}} }

// Currency 多币种钱包；op-id 去重保幂等。仅主循环用，零锁。
type Currency struct {
	balances map[string]int64
	ops      *opDedup
	dirty    bool
}

func NewCurrency() *Currency {
	return &Currency{balances: make(map[string]int64), ops: newOpDedup(maxCurrencyOps)}
}

func (c *Currency) Name() string  { return CurrencyComponentName }
func (c *Currency) Field() string { return CurrencyField }
func (c *Currency) Dirty() bool   { return c.dirty }
func (c *Currency) ClearDirty()   { c.dirty = false }
func (c *Currency) MarkDirty()    { c.dirty = true }

// Load 从存储态恢复（覆盖、清脏）
func (c *Currency) Load(s *CurrencyState) {
	c.balances = make(map[string]int64, len(s.Balances))
	for k, v := range s.Balances {
		c.balances[k] = v
	}
	c.ops.loadFrom(s.Ops)
	c.dirty = false
}

// Snapshot 返回可落库快照（值拷贝）
func (c *Currency) Snapshot() any {
	out := make(map[string]int64, len(c.balances))
	for k, v := range c.balances {
		out[k] = v
	}
	return CurrencyState{Balances: out, Ops: c.ops.snapshot()}
}

// Balance 返回某币种余额
func (c *Currency) Balance(kind string) int64 { return c.balances[kind] }

// Balances 返回余额副本
func (c *Currency) Balances() map[string]int64 {
	out := make(map[string]int64, len(c.balances))
	for k, v := range c.balances {
		out[k] = v
	}
	return out
}

// CanAfford 判断 kind 余额是否 >= amt
func (c *Currency) CanAfford(kind string, amt int64) bool { return c.balances[kind] >= amt }

// Gain 幂等加币：dup→返回当前余额；否则增并标脏、remember。返回(新余额, 是否变更)。
func (c *Currency) Gain(opID, kind string, amt int64) (int64, bool) {
	if c.ops.seen(opID) {
		return c.balances[kind], false
	}
	if amt != 0 {
		c.balances[kind] += amt
		c.dirty = true
	}
	c.ops.remember(opID)
	return c.balances[kind], amt != 0
}

// Spend 幂等扣币：dup→幂等成功(返回当前余额,true)；不足→(当前余额,false)且不 remember（留重试）；
// 足额→扣减、标脏、remember，返回(新余额,true)。
func (c *Currency) Spend(opID, kind string, amt int64) (int64, bool) {
	if c.ops.seen(opID) {
		return c.balances[kind], true
	}
	if c.balances[kind] < amt {
		return c.balances[kind], false
	}
	c.balances[kind] -= amt
	c.dirty = true
	c.ops.remember(opID)
	return c.balances[kind], true
}

// ProtectOp 标记 opID 免于 op-dedup 环淘汰（滞留重放源未消费前）。
func (c *Currency) ProtectOp(opID string) { c.ops.protect(opID) }

// UnprotectOp 解除保护（重放源已 $pull 成功后）。
func (c *Currency) UnprotectOp(opID string) { c.ops.unprotect(opID) }

// 编译期断言 Currency 满足 Component
var _ Component = (*Currency)(nil)
