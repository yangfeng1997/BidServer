// src/servers/lobbysvr/internal/op_dedup.go
package internal

// opDedup 有界 op-id 去重环：seen/remember 两步分离，调用方决定何时 remember
// （如「仅操作成功才记」），空 opID 永不去重。仅主循环用，零锁。
// 受保护 opID（protect）免于 FIFO 淘汰——用于「滞留重放源未消费前 opID 不可丢」（⑤）。
// 约定：protect 须在该 opID 被 remember 前/期间调用（与 replayOffline 一致），
// 使其入环时即受保护、不被淘汰。
type opDedup struct {
	seenSet   map[string]struct{}
	order     []string
	protected map[string]struct{}
	max       int
}

func newOpDedup(max int) *opDedup {
	if max <= 0 {
		max = 128
	}
	return &opDedup{seenSet: make(map[string]struct{}), max: max}
}

// seen 报告 opID 是否已记录（空 opID 恒 false）。
func (o *opDedup) seen(opID string) bool {
	if opID == "" {
		return false
	}
	_, ok := o.seenSet[opID]
	return ok
}

// remember 记录 opID 并维护有界环（超界淘汰最旧的非保护项）；空或重复 opID 是 no-op。
func (o *opDedup) remember(opID string) {
	if opID == "" {
		return
	}
	if _, ok := o.seenSet[opID]; ok {
		return
	}
	o.seenSet[opID] = struct{}{}
	o.order = append(o.order, opID)
	o.evict()
}

// evict 在超界时淘汰最旧的【非保护】opID；全部受保护则暂不淘汰
// （受保护数 = 滞留重放源数，远小于 max，不会无界）。
func (o *opDedup) evict() {
	for len(o.order) > o.max {
		idx := -1
		for i, id := range o.order {
			if _, prot := o.protected[id]; !prot {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		old := o.order[idx]
		o.order = append(o.order[:idx], o.order[idx+1:]...)
		delete(o.seenSet, old)
	}
}

// protect 标记 opID 免于淘汰（重放源消费前调用，须先于/伴随 remember）；空 opID no-op。
func (o *opDedup) protect(opID string) {
	if opID == "" {
		return
	}
	if o.protected == nil {
		o.protected = make(map[string]struct{})
	}
	o.protected[opID] = struct{}{}
}

// unprotect 解除保护并按需补淘汰（重放源已消费/$pull 成功后调用）；空 opID no-op。
func (o *opDedup) unprotect(opID string) {
	if opID == "" {
		return
	}
	delete(o.protected, opID)
	o.evict()
}

// snapshot 返回 op-id 环的有序快照（落库用，值拷贝）。
func (o *opDedup) snapshot() []string {
	out := make([]string, len(o.order))
	copy(out, o.order)
	return out
}

// loadFrom 用持久化的 op-id 序列重建去重环（覆盖现状，维持有界淘汰）。
func (o *opDedup) loadFrom(ops []string) {
	o.seenSet = make(map[string]struct{}, len(ops))
	o.order = o.order[:0]
	o.protected = nil // 重建即清空保护态（保护态由调用方在重放时重新建立）
	for _, id := range ops {
		o.remember(id)
	}
}
