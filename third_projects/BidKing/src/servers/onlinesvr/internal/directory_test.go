package internal

import (
	"testing"
	"time"

	"project/src/common/timewheel"
)

// 手动推进的时间轮：tick=1ms，便于确定性测试过期
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
		t.Fatal("unregister non-existent should report false (idempotent)")
	}
}

func TestDirectory_DupLoginReturnsOld(t *testing.T) {
	d, _ := newTestDir(5 * time.Millisecond)
	d.Register(10001, "1.1.1", "1.2.1", 100)
	old, replaced := d.Register(10001, "1.1.2", "1.2.1", 200) // 不同 gateway
	if !replaced || old == nil || old.GatewayNodeID != "1.1.1" {
		t.Fatalf("dup login should return old gateway entry, got %+v replaced=%v", old, replaced)
	}
	e, _ := d.Query(10001)
	if e.GatewayNodeID != "1.1.2" {
		t.Fatalf("entry should be overwritten to new gateway, got %s", e.GatewayNodeID)
	}
	// 同 gateway 重复注册不算顶号
	_, replaced2 := d.Register(10001, "1.1.2", "1.2.1", 300)
	if replaced2 {
		t.Fatal("same gateway re-register should not be a kick")
	}
}

func TestDirectory_Expire(t *testing.T) {
	d, tw := newTestDir(5 * time.Millisecond) // 5 ticks
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
	tw.Advance() // 2 ticks，未到期
	if !d.Touch(10001, 200) {
		t.Fatal("touch on existing should return true")
	}
	for i := 0; i < 4; i++ { // 再推 4 tick（距 touch 4 ticks，<5，仍在）
		tw.Advance()
	}
	if _, ok := d.Query(10001); !ok {
		t.Fatal("entry should survive within ttl after touch")
	}
	tw.Advance() // 距 touch 第 5 tick，到期
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
	// 重连：换 gateway/lobby 重注册（顶号路径）——room 绑定必须保留
	old, replaced := dir.Register(7, "1.1.2", "1.2.2", time.Now().UnixNano())
	if !replaced || old == nil {
		t.Fatalf("cross-gateway re-register should return old/replaced")
	}
	e, ok := dir.Query(7)
	if !ok || e.GatewayNodeID != "1.1.2" || e.LobbyNodeID != "1.2.2" {
		t.Fatalf("re-register should update gate/lobby, got %+v", e)
	}
	if e.RoomNodeID != "1.7.1" || e.GameID != "1.8.1-1" {
		t.Fatalf("re-register must PRESERVE room binding, got room=%q game=%q", e.RoomNodeID, e.GameID)
	}
}

// TestDirectory_StaleExpireIgnored 验证 spec §6.3「防 Touch/到期竞态」：
// timewheel 在持锁阶段把到期任务出队、释放锁后才回调（见 timewheel.Advance），
// 故一个旧代次的 expire 回调可能在 Register/Touch 顶替条目后才迟到触发；
// 该迟到回调必须被识别为已被顶替并忽略，不能误删新代次条目。
func TestDirectory_StaleExpireIgnored(t *testing.T) {
	d, _ := newTestDir(time.Hour) // 长 ttl + 手动轮：自动过期不触发，由测试直接调 expire
	d.Register(10001, "1.1.1", "1.2.1", 100)
	staleGen := d.genOf[10001] // 第一代代次

	// 顶号：装入新一代 entry+timer（旧代次被顶替，但其回调可能已"在途"）
	d.Register(10001, "1.1.2", "1.2.1", 200)

	// 模拟旧代次回调迟到触发：必须被识别为已被顶替而忽略
	d.expire(10001, staleGen)
	e, ok := d.Query(10001)
	if !ok {
		t.Fatal("stale expire deleted a freshly re-registered entry")
	}
	if e.GatewayNodeID != "1.1.2" {
		t.Fatalf("entry should remain new generation, got gateway=%s", e.GatewayNodeID)
	}

	// 当前代次回调仍能正常过期
	d.expire(10001, d.genOf[10001])
	if _, ok := d.Query(10001); ok {
		t.Fatal("current-generation expire should remove the entry")
	}
}
