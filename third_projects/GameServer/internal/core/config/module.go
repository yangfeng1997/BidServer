package config

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"project/internal/core/app"
)

// Module 配置模块，负责服务配置的 SIGHUP 热更
type Module struct {
	app.BaseModule
	poster      app.Poster
	stopCh      chan struct{}
	reloadTrace chan error // 可选：每次 reload 后发送结果，供测试观察（nil 时不发送）
}

// SetReloadTrace 注入 reload 结果通知通道，供异步测试等待 reload 完成
func (m *Module) SetReloadTrace(ch chan error) { m.reloadTrace = ch }

// NewModule 创建配置模块
func NewModule() *Module {
	return &Module{stopCh: make(chan struct{})}
}

// resolveConfigPath 将路径展开为 yaml 文件列表
func resolveConfigPath(p string) []string {
	info, err := os.Stat(p)
	if err != nil {
		return []string{p} // 保持原样，LoadFiles 会报错
	}
	if !info.IsDir() {
		return []string{p}
	}
	matches, _ := filepath.Glob(filepath.Join(p, "*.yaml"))
	sort.Strings(matches)
	return matches
}

func (m *Module) Init(a *app.App) error {
	m.poster = a
	return nil
}

func (m *Module) AfterInit() error {
	if serviceLoader == nil {
		return fmt.Errorf("config: no service loader registered")
	}
	if err := serviceLoader.Load(); err != nil {
		return err
	}
	if errs := serviceLoader.Validate(); len(errs) > 0 {
		return fmt.Errorf("config validation failed: %v", errs)
	}
	serviceLoader.Swap()

	go m.watchSIGHUP()
	return nil
}

func (m *Module) BeforeStop() {
	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}
}

func (m *Module) Fini() {}

func (m *Module) watchSIGHUP() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	defer signal.Stop(ch)

	for {
		select {
		case <-m.stopCh:
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			m.poster.Post(func() {
				err := m.Reload()
				if m.reloadTrace != nil {
					select {
					case m.reloadTrace <- err:
					default:
					}
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "[config] hot-reload rejected: %v\n", err)
					return
				}
				fmt.Fprintf(os.Stderr, "[config] hot-reload OK\n")
			})
		}
	}
}

// Reload 三段式：Load(shadow) → Check(静态) → Validate(required/enum) → Swap
func (m *Module) Reload() error {
	if serviceLoader == nil {
		return fmt.Errorf("reload: no service loader")
	}
	if err := serviceLoader.Load(); err != nil {
		return fmt.Errorf("reload: %w", err)
	}
	if stale := serviceLoader.Check(); len(stale) > 0 {
		return fmt.Errorf("static fields changed (reload rejected): %v", stale)
	}
	if errs := serviceLoader.Validate(); len(errs) > 0 {
		return fmt.Errorf("reload validation failed: %v", errs)
	}
	serviceLoader.Swap()
	return nil
}

// Loader 由每个服务的 config 子包实现，参与 SIGHUP 三段式热更
type Loader interface {
	Load() error      // 从 files 读取解析到 shadow
	Check() []string  // 比较 shadow 与 current，返回变化的静态字段路径（空=可热更）
	Validate() []string // 校验 shadow 的 required/enum（空=合法）
	Swap()            // 原子发布 shadow 为 current
}

// SplitFiles 把配置目录/文件展开为 yaml 文件列表，再按路径含 /common/ 分成公共组与服务组
func SplitFiles(configDirs []string) (common, service []string) {
	var files []string
	for _, p := range configDirs {
		files = append(files, resolveConfigPath(p)...)
	}
	for _, f := range files {
		if strings.Contains(f, "/common/") {
			common = append(common, f)
		} else {
			service = append(service, f)
		}
	}
	sort.Strings(common)
	sort.Strings(service)
	return
}

var serviceLoader Loader

// RegisterService 由每个服务在 Main 中调用，注册本服务的 Loader（进程内唯一）
func RegisterService(l Loader) {
	if serviceLoader != nil {
		panic("config: service loader already registered")
	}
	serviceLoader = l
}

// ResetForTest 重置全局状态供测试隔离
func ResetForTest() {
	serviceLoader = nil
}

