package internal

import "testing"

type fakeComp struct {
	name  string
	dirty bool
}

func (f *fakeComp) Name() string  { return f.name }
func (f *fakeComp) Field() string { return f.name }
func (f *fakeComp) Snapshot() any { return f.name }
func (f *fakeComp) Dirty() bool   { return f.dirty }
func (f *fakeComp) ClearDirty()   { f.dirty = false }
func (f *fakeComp) MarkDirty()    { f.dirty = true }

func TestPlayer_AddAndGet(t *testing.T) {
	p := NewPlayer(10001)
	c := &fakeComp{name: "x"}
	p.AddComponent(c)
	if p.UID() != 10001 {
		t.Fatalf("uid=%d", p.UID())
	}
	if p.Component("x") != c {
		t.Fatal("component not found")
	}
	if len(p.Components()) != 1 {
		t.Fatalf("components=%d", len(p.Components()))
	}
}

func TestPlayer_DuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate component")
		}
	}()
	p := NewPlayer(1)
	p.AddComponent(&fakeComp{name: "x"})
	p.AddComponent(&fakeComp{name: "x"})
}

func TestPlayer_ComponentsPreserveRegistrationOrder(t *testing.T) {
	p := NewPlayer(1)
	p.AddComponent(&fakeComp{name: "a"})
	p.AddComponent(&fakeComp{name: "b"})
	p.AddComponent(&fakeComp{name: "c"})
	got := p.Components()
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d", len(got), len(want))
	}
	for i, c := range got {
		if c.Name() != want[i] {
			t.Fatalf("order[%d]=%q want %q", i, c.Name(), want[i])
		}
	}
}

func TestPlayer_RoomAffinity(t *testing.T) {
	p := NewPlayer(1)
	if p.RoomAffinity() != nil {
		t.Fatalf("new player should have nil room affinity")
	}
	p.SetRoomAffinity("1.7.1", "1.8.1-1", "gold")
	rb := p.RoomAffinity()
	if rb == nil || rb.roomNodeID != "1.7.1" || rb.gameID != "1.8.1-1" {
		t.Fatalf("set room affinity mismatch: %+v", rb)
	}
	// 绝对写幂等：重复 set 覆盖
	p.SetRoomAffinity("1.7.2", "1.8.1-2", "gold")
	if p.RoomAffinity().roomNodeID != "1.7.2" {
		t.Fatalf("set should overwrite")
	}
	p.ClearRoomAffinity()
	if p.RoomAffinity() != nil {
		t.Fatalf("clear should reset to nil")
	}
}

func TestPlayer_RoomAffinityCurrency(t *testing.T) {
	p := NewPlayer(1)
	p.SetRoomAffinity("1.7.1", "g1", "gold")
	if p.RoomAffinity().currency != "gold" || p.RoomAffinity().gameID != "g1" {
		t.Fatalf("affinity currency/game wrong: %+v", p.RoomAffinity())
	}
}
