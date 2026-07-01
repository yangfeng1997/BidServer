package app

import (
	"reflect"
	"testing"
	"time"

	"project/pkg/taskqueue"
)

type lifecycleModule struct {
	name   string
	events *[]string
	BaseModule
}

func (m *lifecycleModule) Init(*App) error {
	*m.events = append(*m.events, m.name+":Init")
	return nil
}

func (m *lifecycleModule) AfterInit() error {
	*m.events = append(*m.events, m.name+":AfterInit")
	return nil
}

func (m *lifecycleModule) BeforeStop() {
	*m.events = append(*m.events, m.name+":BeforeStop")
}

func (m *lifecycleModule) Fini() {
	*m.events = append(*m.events, m.name+":Fini")
}

// newTestApp 构造无 infra 模块的纯净 *App 使生命周期测试无需配置文件
func newTestApp(tick time.Duration) *App {
	return &App{
		q:            taskqueue.New(0),
		tick:         tick,
		readyTimeout: 10 * time.Second,
		quit:         make(chan struct{}),
		stopped:      make(chan struct{}),
	}
}

func TestAppInitAndFiniLifecycleOrder(t *testing.T) {
	var events []string
	a := newTestApp(0)
	a.Register(&lifecycleModule{name: "m1", events: &events})
	a.Register(&lifecycleModule{name: "m2", events: &events})

	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	a.Fini()

	want := []string{
		"m1:Init", "m2:Init",
		"m1:AfterInit", "m2:AfterInit",
		"m2:BeforeStop", "m1:BeforeStop",
		"m2:Fini", "m1:Fini",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestAppRunDrivesUpdaterUntilFini(t *testing.T) {
	a := newTestApp(time.Millisecond)
	updates := 0
	a.Register(UpdaterFunc(func(time.Duration) {
		updates++
		if updates == 2 {
			a.Fini()
		}
	}))

	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := a.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if updates < 2 {
		t.Fatalf("updates = %d, want at least 2", updates)
	}
}

type UpdaterFunc func(time.Duration)

func (f UpdaterFunc) Init(*App) error        { return nil }
func (f UpdaterFunc) AfterInit() error        { return nil }
func (f UpdaterFunc) BeforeStop()             {}
func (f UpdaterFunc) Fini()                   {}
func (f UpdaterFunc) Update(d time.Duration) { f(d) }

func TestAppNewUsesBaseOptions(t *testing.T) {
	opt := &BaseOptions{
		Tick:         10 * time.Millisecond,
		ReadyTimeout: 5 * time.Second,
		DrainTimeout: 2 * time.Second,
	}
	a := New(opt)

	if a.tick != 10*time.Millisecond {
		t.Fatalf("tick = %v, want 10ms", a.tick)
	}
	if a.readyTimeout != 5*time.Second {
		t.Fatalf("readyTimeout = %v, want 5s", a.readyTimeout)
	}
	if a.drainTimeout != 2*time.Second {
		t.Fatalf("drainTimeout = %v, want 2s", a.drainTimeout)
	}
	if a.q == nil {
		t.Fatal("q is nil")
	}
	if a.quit == nil {
		t.Fatal("quit is nil")
	}
	if a.stopped == nil {
		t.Fatal("stopped is nil")
	}
}

func TestAppRunUsesConfiguredTick(t *testing.T) {
	a := newTestApp(50 * time.Millisecond)
	a.Register(UpdaterFunc(func(dt time.Duration) {
		if dt != 50*time.Millisecond {
			t.Errorf("expected tick 50ms, got %v", dt)
		}
		a.Fini()
	}))

	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := a.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// updater 内验证 tick 已应用，无需额外检查
}

func TestAppRunNoOptsDefaults(t *testing.T) {
	a := newTestApp(0)
	done := make(chan struct{})
	pmod := &postStopModule{done: done, a: a}
	a.Register(pmod)

	if err := a.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := a.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	select {
	case <-done:
	default:
		t.Fatal("module Init was not executed")
	}
}

// postStopModule 在 Init 里通过 App 队列投递 Fini
// 这样 Run() 在零 tick 模式下处理完投递的函数后正常退出
type postStopModule struct {
	BaseModule
	done chan struct{}
	a    *App
}

func (m *postStopModule) Init(*App) error {
	close(m.done)
	m.a.Post(func() { m.a.Fini() })
	return nil
}

