package timewheel

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 测试统一用 tick=1ms、slots=8 的小轮，配合手动 Advance 精确控制时间推进，
// 不依赖 wall-clock，结果稳定。单圈覆盖 8 个 tick

const (
	testTick  = time.Millisecond
	testSlots = 8
)

// advanceN 推进 n 格
func advanceN(tw *TimeWheel, n int) {
	for i := 0; i < n; i++ {
		tw.Advance()
	}
}

func TestAfterFunc(t *testing.T) {
	tw := New(testTick, testSlots)
	var fired atomic.Int32
	// 3 个 tick 后触发
	tw.AfterFunc(3*testTick, func() { fired.Add(1) })

	advanceN(tw, 2)
	if got := fired.Load(); got != 0 {
		t.Fatalf("提前触发：advance 2 格后 fired=%d，期望 0", got)
	}
	tw.Advance() // 第 3 格
	if got := fired.Load(); got != 1 {
		t.Fatalf("未按时触发：advance 3 格后 fired=%d，期望 1", got)
	}
	// 一次性任务不应再触发
	advanceN(tw, testSlots*2)
	if got := fired.Load(); got != 1 {
		t.Fatalf("一次性任务重复触发：fired=%d，期望 1", got)
	}
}

func TestAfterFunc_LongerThanOneRound(t *testing.T) {
	tw := New(testTick, testSlots)
	var fired atomic.Int32
	// 延迟 = 单圈(8) + 3 = 11 个 tick，需要圈数法（rounds=1）
	tw.AfterFunc(time.Duration(testSlots+3)*testTick, func() { fired.Add(1) })

	advanceN(tw, testSlots+2)
	if got := fired.Load(); got != 0 {
		t.Fatalf("跨圈任务提前触发：advance %d 格后 fired=%d，期望 0", testSlots+2, got)
	}
	tw.Advance() // 第 11 格
	if got := fired.Load(); got != 1 {
		t.Fatalf("跨圈任务未按时触发：fired=%d，期望 1", got)
	}
}

func TestTick(t *testing.T) {
	tw := New(testTick, testSlots)
	var fired atomic.Int32
	// 每 2 个 tick 触发一次
	tw.Tick(2*testTick, func() { fired.Add(1) })

	advanceN(tw, 6) // 应触发 3 次（第 2、4、6 格）
	if got := fired.Load(); got != 3 {
		t.Fatalf("周期任务触发次数错误：advance 6 格后 fired=%d，期望 3", got)
	}
}

func TestTick_IntervalEqualsOneRound(t *testing.T) {
	// 周期恰为单圈整数倍：reschedule 会落回同一 bucket，验证不会同帧重复触发
	tw := New(testTick, testSlots)
	var fired atomic.Int32
	tw.Tick(time.Duration(testSlots)*testTick, func() { fired.Add(1) })

	advanceN(tw, testSlots*3) // 3 圈应触发 3 次
	if got := fired.Load(); got != 3 {
		t.Fatalf("整圈周期任务触发次数错误：fired=%d，期望 3", got)
	}
}

func TestStop(t *testing.T) {
	tw := New(testTick, testSlots)
	var fired atomic.Int32
	timer := tw.AfterFunc(3*testTick, func() { fired.Add(1) })

	tw.Advance() // 第 1 格
	tw.Stop(timer)
	advanceN(tw, testSlots*2)
	if got := fired.Load(); got != 0 {
		t.Fatalf("取消后仍触发：fired=%d，期望 0", got)
	}
}

func TestStop_AfterFire(t *testing.T) {
	tw := New(testTick, testSlots)
	var fired atomic.Int32
	timer := tw.AfterFunc(2*testTick, func() { fired.Add(1) })

	advanceN(tw, 2) // 触发
	if got := fired.Load(); got != 1 {
		t.Fatalf("触发次数=%d，期望 1", got)
	}
	// 已触发后再 Stop 应安全无副作用（幂等）
	tw.Stop(timer)
	tw.Stop(timer)
}

func TestStop_PeriodicTask(t *testing.T) {
	tw := New(testTick, testSlots)
	var fired atomic.Int32
	timer := tw.Tick(2*testTick, func() { fired.Add(1) })

	advanceN(tw, 4) // 触发 2 次
	tw.Stop(timer)
	advanceN(tw, testSlots*2)
	if got := fired.Load(); got != 2 {
		t.Fatalf("周期任务取消后仍触发：fired=%d，期望 2", got)
	}
}

func TestLen(t *testing.T) {
	tw := New(testTick, testSlots)
	if tw.Len() != 0 {
		t.Fatalf("空轮 Len=%d，期望 0", tw.Len())
	}
	t1 := tw.AfterFunc(3*testTick, func() {})
	tw.AfterFunc(5*testTick, func() {})
	if tw.Len() != 2 {
		t.Fatalf("加入 2 个任务后 Len=%d，期望 2", tw.Len())
	}
	tw.Stop(t1)
	if tw.Len() != 1 {
		t.Fatalf("取消 1 个后 Len=%d，期望 1", tw.Len())
	}
	// 周期任务触发后应仍在轮中
	tw.Tick(2*testTick, func() {})
	advanceN(tw, 2)
	if tw.Len() != 2 { // 剩 1 个 AfterFunc(5) + 1 个周期任务
		t.Fatalf("触发后 Len=%d，期望 2", tw.Len())
	}
}

// TestStart_SelfDriving 验证自驱动模式：Start 后回调能被自动触发，Close 后停止
func TestStart_SelfDriving(t *testing.T) {
	tw := New(5*time.Millisecond, testSlots)
	var fired atomic.Int32
	tw.AfterFunc(10*time.Millisecond, func() { fired.Add(1) })
	tw.Start()
	defer tw.Close()

	// 给足时间让内部 goroutine 推进（10ms 延迟 + 余量）
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fired.Load() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := fired.Load(); got != 1 {
		t.Fatalf("自驱动未触发：fired=%d，期望 1", got)
	}
}

func TestClose_Idempotent(t *testing.T) {
	tw := New(testTick, testSlots)
	tw.Start()
	tw.Close()
	tw.Close() // 二次 Close 不应 panic
}

// TestConcurrent 在 -race 下验证并发 AfterFunc / Stop / Advance 无数据竞争
func TestConcurrent(t *testing.T) {
	tw := New(testTick, 64)
	stop := make(chan struct{})

	// 推进者：独立于 producers 的 wg，靠 stop 退出，用 doneAdvance 同步收尾
	doneAdvance := make(chan struct{})
	go func() {
		defer close(doneAdvance)
		for {
			select {
			case <-stop:
				return
			default:
				tw.Advance()
			}
		}
	}()

	// 多个注册/取消者
	var producers sync.WaitGroup
	for i := 0; i < 8; i++ {
		producers.Add(1)
		go func() {
			defer producers.Done()
			for j := 0; j < 500; j++ {
				timer := tw.AfterFunc(time.Duration(j%32+1)*testTick, func() {})
				if j%2 == 0 {
					tw.Stop(timer)
				}
			}
		}()
	}

	producers.Wait() // 等所有注册/取消完成
	close(stop)      // 通知推进者退出
	<-doneAdvance    // 等推进者真正退出，确保无残留 goroutine 触发竞争
}
