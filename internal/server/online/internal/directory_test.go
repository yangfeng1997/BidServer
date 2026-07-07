package internal

import (
	"testing"
	"time"

	"project/pkg/timewheel"
)

func newTestDir(ttl time.Duration) (*Directory, *timewheel.TimeWheel) {
	tw := timewheel.New(time.Millisecond, 64)
	return NewDirectory(tw, ttl), tw
}

func TestDirectory_RegisterQueryUnregister(t *testing.T) {
	d, _ := newTestDir(5 * time.Millisecond)
	old, replaced := d.Register(10001, "1.1.1", "1.2.1", 100)
	if replaced || old != nil {
		t.Fatalf("first register should not replace")
	}
	e, ok := d.Query(10001)
	if !ok || e.GatewayNodeID != "1.1.1" || e.LobbyNodeID != "1.2.1" {
		t.Fatalf("query miss/wrong: %+v %v", e, ok)
	}
	if !d.Unregister(10001) {
		t.Fatal("unregister should report removed")
	}
	if _, ok := d.Query(10001); ok {
		t.Fatal("query should miss after unregister")
	}
	if d.Unregister(10001) {
		t.Fatal("unregister non-existent should report false")
	}
}

func TestDirectory_DupLoginReturnsOld(t *testing.T) {
	d, _ := newTestDir(5 * time.Millisecond)
	d.Register(10001, "1.1.1", "1.2.1", 100)
	old, replaced := d.Register(10001, "1.1.2", "1.2.1", 200)
	if !replaced || old == nil || old.GatewayNodeID != "1.1.1" {
		t.Fatalf("dup login should return old gateway entry, got %+v replaced=%v", old, replaced)
	}
	e, _ := d.Query(10001)
	if e.GatewayNodeID != "1.1.2" {
		t.Fatalf("entry should be overwritten to new gateway, got %s", e.GatewayNodeID)
	}
	_, replaced = d.Register(10001, "1.1.2", "1.2.1", 300)
	if replaced {
		t.Fatal("same gateway re-register should not be a kick")
	}
}

func TestDirectory_Expire(t *testing.T) {
	d, tw := newTestDir(5 * time.Millisecond)
	d.Register(10001, "1.1.1", "1.2.1", 100)
	for i := 0; i < 6; i++ {
		tw.Advance()
	}
	if _, ok := d.Query(10001); ok {
		t.Fatal("entry should expire after ttl")
	}
}

func TestDirectory_TouchResetsExpiry(t *testing.T) {
	d, tw := newTestDir(5 * time.Millisecond)
	d.Register(10001, "1.1.1", "1.2.1", 100)
	tw.Advance()
	tw.Advance()
	if !d.Touch(10001, 200) {
		t.Fatal("touch on existing should return true")
	}
	for i := 0; i < 4; i++ {
		tw.Advance()
	}
	if _, ok := d.Query(10001); !ok {
		t.Fatal("entry should survive within ttl after touch")
	}
	tw.Advance()
	tw.Advance()
	if _, ok := d.Query(10001); ok {
		t.Fatal("entry should expire ttl after last touch")
	}
	if d.Touch(99999, 1) {
		t.Fatal("touch on missing should return false")
	}
}

func TestDirectory_BindRoom(t *testing.T) {
	tw := timewheel.New(time.Millisecond, 64)
	dir := NewDirectory(tw, time.Second)
	dir.Register(7, "1.1.1", "1.2.1", time.Now().UnixNano())

	if ok := dir.BindRoom(7, "1.7.1", "1.8.1-1"); !ok {
		t.Fatalf("BindRoom on online entry should succeed")
	}
	e, ok := dir.Query(7)
	if !ok || e.RoomNodeID != "1.7.1" || e.GameID != "1.8.1-1" {
		t.Fatalf("want room=1.7.1 game=1.8.1-1, got %+v", e)
	}

	if ok := dir.UnbindRoom(7); !ok {
		t.Fatalf("UnbindRoom should succeed")
	}
	e, _ = dir.Query(7)
	if e.RoomNodeID != "" || e.GameID != "" {
		t.Fatalf("want cleared room binding, got %+v", e)
	}
}

func TestDirectory_BindRoom_NotOnline(t *testing.T) {
	tw := timewheel.New(time.Millisecond, 64)
	dir := NewDirectory(tw, time.Second)
	if ok := dir.BindRoom(99, "1.7.1", "g"); ok {
		t.Fatalf("BindRoom on absent entry should return false")
	}
}

func TestDirectory_RegisterPreservesRoomBinding(t *testing.T) {
	tw := timewheel.New(time.Millisecond, 64)
	dir := NewDirectory(tw, time.Second)
	dir.Register(7, "1.1.1", "1.2.1", time.Now().UnixNano())
	if !dir.BindRoom(7, "1.7.1", "1.8.1-1") {
		t.Fatalf("BindRoom should succeed")
	}
	old, replaced := dir.Register(7, "1.1.2", "1.2.2", time.Now().UnixNano())
	if !replaced || old == nil {
		t.Fatalf("cross-gateway re-register should return old/replaced")
	}
	e, ok := dir.Query(7)
	if !ok || e.GatewayNodeID != "1.1.2" || e.LobbyNodeID != "1.2.2" {
		t.Fatalf("re-register should update gate/lobby, got %+v", e)
	}
	if e.RoomNodeID != "1.7.1" || e.GameID != "1.8.1-1" {
		t.Fatalf("re-register must preserve room binding, got room=%q game=%q", e.RoomNodeID, e.GameID)
	}
}

func TestDirectory_StaleExpireIgnored(t *testing.T) {
	d, _ := newTestDir(time.Hour)
	d.Register(10001, "1.1.1", "1.2.1", 100)
	staleGen := d.genOf[10001]
	d.Register(10001, "1.1.2", "1.2.1", 200)
	d.expire(10001, staleGen)
	e, ok := d.Query(10001)
	if !ok {
		t.Fatal("stale expire deleted a freshly re-registered entry")
	}
	if e.GatewayNodeID != "1.1.2" {
		t.Fatalf("entry should remain new generation, got gateway=%s", e.GatewayNodeID)
	}
	d.expire(10001, d.genOf[10001])
	if _, ok := d.Query(10001); ok {
		t.Fatal("current-generation expire should remove the entry")
	}
}
