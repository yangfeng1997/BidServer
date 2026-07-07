package internal

import "testing"

func TestOpDedup_SeenRemember(t *testing.T) {
	o := newOpDedup(3)
	if o.seen("a") {
		t.Fatal("unseen should be false")
	}
	o.remember("a")
	if !o.seen("a") {
		t.Fatal("remembered should be seen")
	}
}

func TestOpDedup_EmptyNeverDedups(t *testing.T) {
	o := newOpDedup(3)
	o.remember("")
	if o.seen("") {
		t.Fatal("empty opID must never be seen")
	}
}

func TestOpDedup_BoundedEviction(t *testing.T) {
	o := newOpDedup(2)
	o.remember("a")
	o.remember("b")
	o.remember("c") // 淘汰最旧 a
	if o.seen("a") {
		t.Fatal("a should be evicted")
	}
	if !o.seen("b") || !o.seen("c") {
		t.Fatal("b,c should remain")
	}
}

func TestOpDedup_RememberIdempotent(t *testing.T) {
	o := newOpDedup(2)
	o.remember("a")
	o.remember("a") // 重复 remember 不重复入队（否则环计数错乱）
	o.remember("b")
	if !o.seen("a") || !o.seen("b") {
		t.Fatal("a,b should both remain (no double-count)")
	}
}

func TestOpDedup_SnapshotLoadRoundtrip(t *testing.T) {
	o := newOpDedup(128)
	o.remember("a")
	o.remember("b")
	snap := o.snapshot()
	if len(snap) != 2 || snap[0] != "a" || snap[1] != "b" {
		t.Fatalf("snapshot mismatch: %v", snap)
	}
	o2 := newOpDedup(128)
	o2.loadFrom(snap)
	if !o2.seen("a") || !o2.seen("b") || o2.seen("c") {
		t.Fatalf("loadFrom did not rebuild dedup set")
	}
}

func TestOpDedup_LoadFromRespectsBound(t *testing.T) {
	o := newOpDedup(2)
	o.loadFrom([]string{"a", "b", "c"}) // 超界：淘汰最旧 a
	if o.seen("a") || !o.seen("b") || !o.seen("c") {
		t.Fatalf("loadFrom should keep bound, evicting oldest")
	}
}

func TestOpDedup_ProtectedSurvivesEviction(t *testing.T) {
	o := newOpDedup(2)
	o.protect("g") // 保护先于 remember（与真实 replayOffline 顺序一致）
	o.remember("g")
	for i := 0; i < 10; i++ {
		o.remember(string(rune('A' + i)))
	}
	if !o.seen("g") {
		t.Fatal("protected opID must NOT be evicted")
	}
	if o.seen("A") {
		t.Fatal("oldest non-protected opID should have been evicted")
	}
	snap := o.snapshot()
	found := false
	for _, id := range snap {
		if id == "g" {
			found = true
		}
	}
	if !found {
		t.Fatal("snapshot must retain protected opID")
	}
}

func TestOpDedup_UnprotectAllowsEviction(t *testing.T) {
	o := newOpDedup(2)
	// 3 个受保护项使环超界（保护期间受保护项不被淘汰）
	for _, id := range []string{"g1", "g2", "g3"} {
		o.protect(id)
		o.remember(id)
	}
	if !o.seen("g1") || !o.seen("g2") || !o.seen("g3") {
		t.Fatal("all protected retained even over max")
	}
	o.unprotect("g1") // g1 恢复可淘汰，环超界 → 淘汰 g1（最旧非保护）
	if o.seen("g1") {
		t.Fatal("after unprotect, over-bound ring should evict the now-unprotected oldest (g1)")
	}
	if !o.seen("g2") || !o.seen("g3") {
		t.Fatal("still-protected opIDs must remain")
	}
}

func TestOpDedup_AllProtectedDoesNotPanicOrUnbound(t *testing.T) {
	o := newOpDedup(1)
	o.protect("a")
	o.remember("a")
	o.protect("b")
	o.remember("b") // 全保护，超界但不淘汰受保护项、不 panic
	if !o.seen("a") || !o.seen("b") {
		t.Fatal("all-protected entries must be retained")
	}
}
