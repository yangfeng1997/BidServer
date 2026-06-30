package syncmap

import "sync"

// Map 是对 sync.Map 的泛型封装，消除调用方的类型断言。
type Map[K comparable, V any] struct {
	m sync.Map
}

func (m *Map[K, V]) Store(key K, val V) {
	m.m.Store(key, val)
}

func (m *Map[K, V]) Load(key K) (V, bool) {
	v, ok := m.m.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	return v.(V), true
}

// Swap 存入新值并返回旧值（若存在）
func (m *Map[K, V]) Swap(key K, val V) (V, bool) {
	old, loaded := m.m.Swap(key, val)
	if !loaded {
		var zero V
		return zero, false
	}
	return old.(V), true
}

// LoadOrStore 若 key 不存在则存入 val，返回实际存储的值和是否为已有值
func (m *Map[K, V]) LoadOrStore(key K, val V) (V, bool) {
	actual, loaded := m.m.LoadOrStore(key, val)
	return actual.(V), loaded
}

func (m *Map[K, V]) Delete(key K) {
	m.m.Delete(key)
}

// Range 遍历所有键值对，f 返回 false 时停止
func (m *Map[K, V]) Range(f func(K, V) bool) {
	m.m.Range(func(k, v any) bool {
		return f(k.(K), v.(V))
	})
}

// LoadAndDelete 读取并删除，若不存在返回零值和 false
func (m *Map[K, V]) LoadAndDelete(key K) (V, bool) {
	v, loaded := m.m.LoadAndDelete(key)
	if !loaded {
		var zero V
		return zero, false
	}
	return v.(V), true
}
