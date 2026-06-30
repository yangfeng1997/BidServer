package jumphash

import (
	"fmt"
	"testing"
)

func TestJump_Range(t *testing.T) {
	for _, n := range []int{1, 2, 5, 16, 1024} {
		for k := uint64(0); k < 1000; k++ {
			b := Jump(k, n)
			if b < 0 || int(b) >= n {
				t.Fatalf("Jump(%d,%d)=%d out of [0,%d)", k, n, b, n)
			}
		}
	}
	if got := Jump(123, 0); got != -1 {
		t.Fatalf("Jump with 0 buckets = %d, want -1", got)
	}
}

func TestPick_EmptyAndStable(t *testing.T) {
	if _, ok := Pick(nil, "u1"); ok {
		t.Fatal("Pick on empty members should return ok=false")
	}
	members := []string{"1.5.3", "1.5.1", "1.5.2"}
	got1, ok := Pick(members, "uid-42")
	if !ok {
		t.Fatal("expected ok")
	}
	// 乱序输入应得相同结果（内部排序，跨实例一致）
	got2, _ := Pick([]string{"1.5.2", "1.5.3", "1.5.1"}, "uid-42")
	if got1 != got2 {
		t.Fatalf("Pick not order-independent: %s vs %s", got1, got2)
	}
	// 去重：重复成员不影响结果
	got3, _ := Pick([]string{"1.5.1", "1.5.2", "1.5.3", "1.5.2"}, "uid-42")
	if got1 != got3 {
		t.Fatalf("Pick not dedup-stable: %s vs %s", got1, got3)
	}
}

func TestPick_TailAddMovesFew(t *testing.T) {
	base := []string{"1.5.1", "1.5.2", "1.5.3", "1.5.4"}
	// 在排序尾部追加一个成员（"1.5.5" 排在最后）
	grown := append([]string{}, base...)
	grown = append(grown, "1.5.5")
	moved := 0
	const total = 10000
	for i := 0; i < total; i++ {
		key := fmt.Sprintf("uid-%d", i)
		a, _ := Pick(base, key)
		b, _ := Pick(grown, key)
		if a != b {
			moved++
		}
	}
	// Jump Hash 尾部增节点理论迁移 ~1/N（N=5 → 20%），留宽松上界 30%
	if moved > total*30/100 {
		t.Fatalf("tail add moved %d/%d keys, expected ~1/N", moved, total)
	}
}
