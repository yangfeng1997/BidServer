package config

import (
	"sync"
	"sync/atomic"
)

type ReloadCheck[T any] func(candidate *T, current *T) error

type ConfigEntry[T any] struct {
	path      string
	load      func(string) (*T, error)
	check     ReloadCheck[T]
	reloadMtx sync.Mutex
	data      atomic.Pointer[T]
}

func NewConfigEntry[T any](path string, load func(string) (*T, error), check ReloadCheck[T]) (*ConfigEntry[T], error) {
	entry := &ConfigEntry[T]{path: path, load: load, check: check}
	if err := entry.Reload(); err != nil {
		return nil, err
	}
	return entry, nil
}

func (entry *ConfigEntry[T]) Get() *T {
	return entry.data.Load()
}

func (entry *ConfigEntry[T]) Path() string {
	return entry.path
}

func (entry *ConfigEntry[T]) Reload() error {
	entry.reloadMtx.Lock()
	defer entry.reloadMtx.Unlock()

	candidate, err := entry.load(entry.path)
	if err != nil {
		return err
	}
	current := entry.data.Load()
	if current != nil && entry.check != nil {
		if err := entry.check(candidate, current); err != nil {
			return err
		}
	}
	entry.data.Store(candidate)
	return nil
}
