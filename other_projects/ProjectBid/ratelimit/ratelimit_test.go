package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestTokenBucketAllow(t *testing.T) {
	tb := NewTokenBucket(100, 10)
	// 初始应允许 burst=10 次
	for i := 0; i < 10; i++ {
		if !tb.Allow("") {
			t.Errorf("第 %d 次应该被允许", i+1)
		}
	}
	// 第 11 次应该拒绝
	if tb.Allow("") {
		t.Error("超出 burst 应该被拒绝")
	}
}

func TestTokenBucketRefill(t *testing.T) {
	tb := NewTokenBucket(100, 1)
	if !tb.Allow("") {
		t.Fatal("第一次应该被允许")
	}
	if tb.Allow("") {
		t.Fatal("第二次（未补充时）应该被拒绝")
	}
	// 等待足够时间补充令牌
	time.Sleep(15 * time.Millisecond)
	if !tb.Allow("") {
		t.Error("补充后应该被允许")
	}
}

func TestSlidingWindowAllow(t *testing.T) {
	sw := NewSlidingWindow(3, time.Second)
	// 同一 key 在窗口内最多 3 次
	for i := 0; i < 3; i++ {
		if !sw.Allow("user1") {
			t.Errorf("第 %d 次应该被允许", i+1)
		}
	}
	if sw.Allow("user1") {
		t.Error("超过 limit 应该被拒绝")
	}
	// 不同 key 不受影响
	if !sw.Allow("user2") {
		t.Error("不同 key 应该被允许")
	}
}

func TestSlidingWindowReset(t *testing.T) {
	sw := NewSlidingWindow(2, 50*time.Millisecond)
	sw.Allow("a")
	sw.Allow("a")
	if sw.Allow("a") {
		t.Error("窗口满时应该被拒绝")
	}
	time.Sleep(60 * time.Millisecond)
	if !sw.Allow("a") {
		t.Error("窗口过期后应该被允许")
	}
}

func TestRateLimitConcurrency(t *testing.T) {
	tb := NewTokenBucket(1000, 100)
	var wg sync.WaitGroup
	errs := make(chan bool, 1000)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				errs <- tb.Allow("")
			}
		}()
	}
	wg.Wait()
	close(errs)
	allowed := 0
	for ok := range errs {
		if ok {
			allowed++
		}
	}
	// 1000 次请求，burst=100，不应全部通过，也不应全部拒绝
	if allowed == 0 || allowed == 1000 {
		t.Errorf("并发测试异常: allowed=%d", allowed)
	}
}
