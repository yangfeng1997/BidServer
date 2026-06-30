package internal

import "testing"

func TestBag_LoadAndSnapshot(t *testing.T) {
	b := NewBag()
	b.Load(&BagState{Items: map[string]int32{"100": 5, "200": 3}})
	if b.Count(100) != 5 || b.Count(200) != 3 {
		t.Fatalf("load wrong: %v", b.Items())
	}
	if b.Dirty() {
		t.Fatal("freshly loaded bag should be clean")
	}
	snap := b.Snapshot().(BagState)
	if snap.Items["100"] != 5 {
		t.Fatalf("snapshot wrong: %v", snap.Items)
	}
}

func TestBag_DirtyLifecycle(t *testing.T) {
	b := NewBag()
	b.MarkDirty()
	if !b.Dirty() {
		t.Fatal("MarkDirty")
	}
	b.ClearDirty()
	if b.Dirty() {
		t.Fatal("ClearDirty")
	}
}

func TestBag_ComponentNames(t *testing.T) {
	b := NewBag()
	if b.Name() != BagComponentName || b.Field() != BagField {
		t.Fatalf("names: %s/%s", b.Name(), b.Field())
	}
}

func TestBag_Load_SkipsMalformedKeys(t *testing.T) {
	b := NewBag()
	b.Load(&BagState{Items: map[string]int32{"100": 5, "abc": 9}})
	if b.Count(100) != 5 {
		t.Fatalf("valid key not loaded: %v", b.Items())
	}
	if len(b.Items()) != 1 {
		t.Fatalf("malformed key should be skipped, got %v", b.Items())
	}
}

func TestBag_Add_MarksDirtyAndAccumulates(t *testing.T) {
	b := NewBag()
	if got := b.Add("op1", 100, 5); got != 5 {
		t.Fatalf("add returned %d", got)
	}
	if !b.Dirty() {
		t.Fatal("add should mark dirty")
	}
	if got := b.Add("op2", 100, 3); got != 8 {
		t.Fatalf("accumulate returned %d", got)
	}
}

func TestBag_Add_IdempotentByOpID(t *testing.T) {
	b := NewBag()
	b.Add("op1", 100, 5)
	if got := b.Add("op1", 100, 5); got != 5 { // 同 op-id 重试不双加
		t.Fatalf("duplicate op double-added: %d", got)
	}
	if b.Count(100) != 5 {
		t.Fatalf("count after dup: %d", b.Count(100))
	}
}

func TestBag_Add_NegativeRemoves(t *testing.T) {
	b := NewBag()
	b.Add("op1", 100, 5)
	b.Add("op2", 100, -5)
	if _, ok := b.Items()[100]; ok {
		t.Fatal("zero-count item should be removed")
	}
}

func TestBag_OpsPersistRoundtrip(t *testing.T) {
	b := NewBag()
	b.Add("op1", 100, 2)
	snap := b.Snapshot().(BagState)
	if len(snap.Ops) != 1 {
		t.Fatalf("want 1 persisted op, got %v", snap.Ops)
	}
	b2 := NewBag()
	b2.Load(&snap)
	if n := b2.Add("op1", 100, 999); n != 2 {
		t.Fatalf("op1 should be deduped after Load, count=%d", n)
	}
}
