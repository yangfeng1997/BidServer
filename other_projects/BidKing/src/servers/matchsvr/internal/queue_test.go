package internal

import (
	"testing"
	"time"
)

func TestMatchQueue_DedupEnqueue(t *testing.T) {
	q := newMatchQueue(2, 200)
	if !q.Enqueue(waiting{uid: 1, reqID: "r1", mmr: 1000}) {
		t.Fatalf("first enqueue should be new")
	}
	if q.Enqueue(waiting{uid: 1, reqID: "r1", mmr: 1000}) {
		t.Fatalf("duplicate (uid,reqId) should not re-enqueue")
	}
	if q.Len() != 1 {
		t.Fatalf("want 1 waiting, got %d", q.Len())
	}
}

func TestMatchQueue_FormTableWithinWindow(t *testing.T) {
	q := newMatchQueue(2, 200)
	q.Enqueue(waiting{uid: 1, reqID: "r1", mmr: 1000})
	if _, ok := q.FormTable(); ok {
		t.Fatalf("one player should not form a table")
	}
	q.Enqueue(waiting{uid: 2, reqID: "r2", mmr: 1100}) // diff 100 <= 200
	table, ok := q.FormTable()
	if !ok || len(table) != 2 {
		t.Fatalf("two in-window players should form a table, got ok=%v n=%d", ok, len(table))
	}
	if q.Len() != 0 {
		t.Fatalf("formed players should be removed, remaining %d", q.Len())
	}
}

func TestMatchQueue_OutOfWindowNoTable(t *testing.T) {
	q := newMatchQueue(2, 200)
	q.Enqueue(waiting{uid: 1, reqID: "r1", mmr: 1000})
	q.Enqueue(waiting{uid: 2, reqID: "r2", mmr: 1500}) // diff 500 > 200
	if _, ok := q.FormTable(); ok {
		t.Fatalf("out-of-window players should not form a table")
	}
	if q.Len() != 2 {
		t.Fatalf("both should remain queued, got %d", q.Len())
	}
}

func TestMatchQueue_FormTablePicksClosest(t *testing.T) {
	q := newMatchQueue(2, 200)
	q.Enqueue(waiting{uid: 1, reqID: "r1", mmr: 1000})
	q.Enqueue(waiting{uid: 2, reqID: "r2", mmr: 1900}) // 远
	q.Enqueue(waiting{uid: 3, reqID: "r3", mmr: 1050}) // 与 1 近
	table, ok := q.FormTable()
	if !ok || len(table) != 2 {
		t.Fatalf("should form table from closest pair")
	}
	// 1 与 3 成桌，2 留队
	if q.Len() != 1 {
		t.Fatalf("one player should remain, got %d", q.Len())
	}
}

func TestMatchQueue_Requeue(t *testing.T) {
	q := newMatchQueue(2, 200)
	w := waiting{uid: 1, reqID: "r1", mmr: 1000}
	q.Enqueue(w)
	q.Enqueue(waiting{uid: 2, reqID: "r2", mmr: 1000})
	table, _ := q.FormTable()
	for _, x := range table {
		q.Requeue(x) // 开局失败放回
	}
	if q.Len() != 2 {
		t.Fatalf("requeued players should be back in queue, got %d", q.Len())
	}
	// 已在 seen，重投不重复入队
	if q.Enqueue(w) {
		t.Fatalf("requeued player still in seen set; redelivery should not re-enqueue")
	}
}

func TestMatchQueue_RejectSecondEntrySameUidWhileWaiting(t *testing.T) {
	q := newMatchQueue(2, 200)
	if !q.Enqueue(waiting{uid: 1, reqID: "a", mmr: 1000}) {
		t.Fatalf("first enqueue should be new")
	}
	// 同一 uid 不同 reqId，仍在等待队列中 → 拒绝
	if q.Enqueue(waiting{uid: 1, reqID: "b", mmr: 1000}) {
		t.Fatalf("second entry for an already-waiting uid should be rejected")
	}
	if q.Len() != 1 {
		t.Fatalf("want 1 waiting, got %d", q.Len())
	}
}

// P4b-1：成桌后 uid 进 pending，残余双发窗口内同 uid 再入被拒（不再「allowed after formed」）。
func TestMatchQueue_SameUidBlockedWhilePending(t *testing.T) {
	q := newMatchQueue(2, 200)
	q.Enqueue(waiting{uid: 1, reqID: "a", mmr: 1000})
	q.Enqueue(waiting{uid: 2, reqID: "c", mmr: 1000})
	if _, ok := q.FormTable(); !ok { // 移除 uid 1、2，进 pendingUids
		t.Fatal("should form table")
	}
	if q.Enqueue(waiting{uid: 1, reqID: "b", mmr: 1000}) {
		t.Fatal("same uid must be blocked while pending (P4b-1 closes the residual double-send window)")
	}
}

func TestMatchQueue_ReapExpired(t *testing.T) {
	q := newMatchQueue(2, 200)
	now := time.Now()
	q.enqueueAt(waiting{uid: 1, reqID: "a", mmr: 1000}, now.Add(-10*time.Second))
	q.enqueueAt(waiting{uid: 2, reqID: "b", mmr: 1000}, now)
	expired := q.ReapExpired(now, 5*time.Second)
	if len(expired) != 1 || expired[0].uid != 1 {
		t.Fatalf("only uid 1 should expire, got %+v", expired)
	}
	if q.waitingUids[1] {
		t.Fatalf("expired uid removed from waitingUids")
	}
	if !q.seen[dedupKey(1, "a")] {
		t.Fatalf("seen retained to block reqId replay")
	}
}

func TestMatchQueue_PendingBlocksDoubleSend(t *testing.T) {
	q := newMatchQueue(2, 200)
	q.Enqueue(waiting{uid: 1, reqID: "a", mmr: 1000})
	q.Enqueue(waiting{uid: 2, reqID: "b", mmr: 1000})
	table, ok := q.FormTable()
	if !ok || len(table) != 2 {
		t.Fatalf("should form table")
	}
	// 成桌后 uid 进 pending：同 uid 新 reqId 应被拒
	if q.Enqueue(waiting{uid: 1, reqID: "c", mmr: 1000}) {
		t.Fatalf("uid in pending must be rejected (double-send window)")
	}
	// 清 pending 后可再入
	q.clearPending(1)
	if !q.Enqueue(waiting{uid: 1, reqID: "c", mmr: 1000}) {
		t.Fatalf("after clearPending, uid can re-enqueue")
	}
}

func TestMatchQueue_RequeueRestoresUidGuard(t *testing.T) {
	q := newMatchQueue(2, 200)
	q.Enqueue(waiting{uid: 1, reqID: "a", mmr: 1000})
	q.Enqueue(waiting{uid: 2, reqID: "b", mmr: 1000})
	table, _ := q.FormTable() // 移除 1、2
	for _, x := range table {
		q.Requeue(x) // 放回 1、2，恢复 waitingUids
	}
	if q.Len() != 2 {
		t.Fatalf("want 2 requeued, got %d", q.Len())
	}
	// uid 1 已放回等待队列 → 新 reqId 再发起被拒
	if q.Enqueue(waiting{uid: 1, reqID: "c", mmr: 1000}) {
		t.Fatalf("requeued uid still waiting; a new reqId should be rejected")
	}
}
