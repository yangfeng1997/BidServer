package timer

import (
	"testing"
	"time"
)

func TestAfterFunc(t *testing.T) {
	tw := NewTimeWheel(10*time.Millisecond, 20)
	tw.Start()
	defer tw.Stop()

	done := make(chan bool, 1)
	tw.AfterFunc(50*time.Millisecond, func() {
		done <- true
	})

	select {
	case <-done:
		// 正常触发
	case <-time.After(2 * time.Second):
		t.Fatal("定时器未在预期时间内触发")
	}
}

func TestAfterFuncCancel(t *testing.T) {
	tw := NewTimeWheel(10*time.Millisecond, 20)
	tw.Start()
	defer tw.Stop()

	fired := make(chan bool, 1)
	timer := tw.AfterFunc(100*time.Millisecond, func() {
		fired <- true
	})
	timer.Stop()

	select {
	case <-fired:
		t.Error("已停止的定时器不应触发")
	case <-time.After(200 * time.Millisecond):
		// 未触发，符合预期
	}
}

func TestAddEveryFunc(t *testing.T) {
	tw := NewTimeWheel(10*time.Millisecond, 20)
	tw.Start()
	defer tw.Stop()

	count := make(chan int, 5)
	tw.AddEveryFunc(20*time.Millisecond, func() {
		count <- 1
	})

	received := 0
	for received < 3 {
		select {
		case <-count:
			received++
		case <-time.After(2 * time.Second):
			t.Fatalf("只收到 %d 次触发", received)
		}
	}
}

func TestCronSchedule(t *testing.T) {
	cs, err := NewCronSchedule("*/1 * * * *")
	if err != nil {
		t.Fatalf("cron 解析失败: %v", err)
	}
	next := cs.Next(time.Now())
	if next.IsZero() {
		t.Error("cron 应返回有效的下一次时间")
	}
}

func TestCronInvalid(t *testing.T) {
	_, err := NewCronSchedule("invalid")
	if err == nil {
		t.Error("无效 cron 应返回错误")
	}
}

func TestCronEverySecond(t *testing.T) {
	cs, err := NewCronSchedule("* * * * *")
	if err != nil {
		t.Fatalf("cron 解析失败: %v", err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	next := cs.Next(now)
	if next != now.Add(time.Minute) {
		t.Errorf("每分钟一次的 cron，下个时间应为 +1min，实际 %v", next)
	}
}
