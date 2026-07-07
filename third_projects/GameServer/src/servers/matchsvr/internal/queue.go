package internal

import (
	"fmt"
	"sort"
	"time"
)

// waiting 等待匹配的玩家
type waiting struct {
	uid        int64
	reqID      string
	mmr        int64
	lobbyNode  string
	enqueuedAt time.Time // 入队时间（用于超时 reap）
}

// matchQueue MMR 匹配队列（仅主循环用，零锁）：(uid,reqId) 去重 + 滑窗凑桌。
// 注：dedup set 为 matchsvr 会话内内存态（纯内存无持久），足够覆盖 JetStream 重投；
// 持久去重（跨重启）不在 P4a。seen 不随成桌清除——防同一请求成桌后被重投再处理。
type matchQueue struct {
	waitingList []waiting
	seen        map[string]bool // key=uid:reqId
	waitingUids map[int64]bool  // 当前在等待队列中的 uid（Enqueue 加 / removeAll 删 / Requeue 恢复），防同一 uid 并发多次入队
	pendingUids map[int64]bool  // 成桌→GameStarted 回告未落定期间的 uid（P4b-1：封堵残余双发窗口）
	size        int             // 成桌人数 N
	window      int64           // MMR 窗口 W
}

func newMatchQueue(size int, window int64) *matchQueue {
	if size < 1 {
		size = 2
	}
	return &matchQueue{
		seen:        make(map[string]bool),
		waitingUids: make(map[int64]bool),
		pendingUids: make(map[int64]bool),
		size:        size,
		window:      window,
	}
}

func dedupKey(uid int64, reqID string) string { return fmt.Sprintf("%d:%s", uid, reqID) }

// enqueueAt 测试用：以指定入队时间入队（绕过 time.Now）。
// 包含全部三重守护：seen / waitingUids / pendingUids。
func (q *matchQueue) enqueueAt(w waiting, at time.Time) bool {
	k := dedupKey(w.uid, w.reqID)
	if q.seen[k] || q.waitingUids[w.uid] || q.pendingUids[w.uid] {
		return false
	}
	w.enqueuedAt = at
	q.seen[k] = true
	q.waitingUids[w.uid] = true
	q.waitingList = append(q.waitingList, w)
	return true
}

// Enqueue 去重入队；新入队返回 true，以下三种情况返回 false（仍应 ack）：
//  1. 同一 (uid,reqId) 重投——seen 已有该 key；
//  2. 同一 uid 已在等待队列（不同 reqId 的并发再次发起）——避免同 uid 被凑进两桌/同桌自匹配；
//  3. 同一 uid 处于 pending（成桌→GameStarted 回告未落定），封堵残余双发窗口（P4b-1）。
func (q *matchQueue) Enqueue(w waiting) bool { return q.enqueueAt(w, time.Now()) }

// clearPending 编排成功后清除 uid 的 pending 守护（GameStarted 已 ack ⇒ lobby roomAffinity 已置，后续 lobby 侧即拒）。
func (q *matchQueue) clearPending(uid int64) { delete(q.pendingUids, uid) }

// Requeue 把成桌后开局失败的玩家放回等待队列（不动 seen，已在其中）。
// 先清除 pendingUids，再恢复 waitingUids 守护，令后续同 uid 新请求在 uid 重回等待队列期间仍被拒绝。
// 重置 enqueuedAt，令等待时钟从回队时刻重新计算。
func (q *matchQueue) Requeue(w waiting) {
	delete(q.pendingUids, w.uid) // 开局失败：退出 pending，改由 waitingUids 守护
	q.waitingUids[w.uid] = true
	w.enqueuedAt = time.Now() // 重置等待时钟
	q.waitingList = append(q.waitingList, w)
}

// Len 当前等待人数
func (q *matchQueue) Len() int { return len(q.waitingList) }

// FormTable 尝试凑齐 size 个 MMR 窗口内（max-min<=window）的玩家：
// 按 mmr 排序滑窗，命中则从等待队列移除并返回该桌；不足/无窗口返回 (nil,false)。
func (q *matchQueue) FormTable() ([]waiting, bool) {
	if len(q.waitingList) < q.size {
		return nil, false
	}
	sorted := make([]waiting, len(q.waitingList))
	copy(sorted, q.waitingList)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].mmr < sorted[j].mmr })
	for i := 0; i+q.size <= len(sorted); i++ {
		if sorted[i+q.size-1].mmr-sorted[i].mmr <= q.window {
			table := make([]waiting, q.size)
			copy(table, sorted[i:i+q.size])
			q.removeAll(table)
			return table, true
		}
	}
	return nil, false
}

// ReapExpired 移除等待超过 maxWait 的玩家（保留 seen 防同 reqId 复活），返回被移除者。
func (q *matchQueue) ReapExpired(now time.Time, maxWait time.Duration) []waiting {
	if len(q.waitingList) == 0 {
		return nil
	}
	var expired []waiting
	kept := q.waitingList[:0]
	for _, w := range q.waitingList {
		if now.Sub(w.enqueuedAt) >= maxWait {
			expired = append(expired, w)
			delete(q.waitingUids, w.uid)
		} else {
			kept = append(kept, w)
		}
	}
	q.waitingList = kept
	return expired
}

// removeAll 从等待队列移除 table 中的玩家（按 (uid,reqId) 匹配），
// 清除 waitingUids 并将其纳入 pendingUids（成桌→GameStarted 回告未落定期间封堵双发，P4b-1）。
func (q *matchQueue) removeAll(table []waiting) {
	drop := make(map[string]bool, len(table))
	for _, w := range table {
		drop[dedupKey(w.uid, w.reqID)] = true
		delete(q.waitingUids, w.uid)
		q.pendingUids[w.uid] = true // 编排中（成桌→GameStarted 回告未落定）
	}
	kept := q.waitingList[:0]
	for _, w := range q.waitingList {
		if !drop[dedupKey(w.uid, w.reqID)] {
			kept = append(kept, w)
		}
	}
	q.waitingList = kept
}
