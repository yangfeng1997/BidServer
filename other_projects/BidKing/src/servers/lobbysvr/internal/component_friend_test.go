package internal

import "testing"

func TestFriend_AddRemoveHasList(t *testing.T) {
	f := NewFriend()
	if f.Has(2) {
		t.Fatal("empty")
	}
	if !f.Add(2) || f.Add(2) { // 首次加返回 true，重复加返回 false（已存在）
		t.Fatal("Add idempotent semantics")
	}
	if !f.Has(2) || !f.Dirty() {
		t.Fatal("after add")
	}
	f.ClearDirty()
	if !f.Remove(2) || f.Remove(2) {
		t.Fatal("Remove semantics")
	}
	if f.Has(2) || !f.Dirty() {
		t.Fatal("after remove")
	}
}

func TestFriend_LoadSnapshot(t *testing.T) {
	f := NewFriend()
	f.Load(&FriendState{Friends: []int64{5, 9}})
	if !f.Has(5) || !f.Has(9) || f.Dirty() {
		t.Fatal("load")
	}
	snap := f.Snapshot().(FriendState)
	if len(snap.Friends) != 2 {
		t.Fatalf("snapshot: %v", snap.Friends)
	}
	if f.Name() != FriendComponentName || f.Field() != FriendField {
		t.Fatal("names")
	}
}
