package agent

import "testing"

func TestMap_RangeVisitsAll(t *testing.T) {
	m := NewMap()
	a := &connAgent{}
	m.Store(1, a)
	m.Store(2, a)
	m.Store(3, a)

	seen := map[int64]bool{}
	m.Range(func(id int64, _ Agent) bool {
		seen[id] = true
		return true
	})
	for _, id := range []int64{1, 2, 3} {
		if !seen[id] {
			t.Fatalf("Range missed key %d", id)
		}
	}
}

func TestMap_RangeEarlyStop(t *testing.T) {
	m := NewMap()
	a := &connAgent{}
	m.Store(1, a)
	m.Store(2, a)

	count := 0
	m.Range(func(int64, Agent) bool {
		count++
		return false // 第一个就停
	})
	if count != 1 {
		t.Fatalf("Range should stop after f returns false, visited %d", count)
	}
}
