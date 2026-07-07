package application

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"project/src/common/serialize/json"
	"project/src/framework/agent"
	"project/src/framework/cluster"
	"project/src/framework/session"
)

// fakeDieCluster 嵌入 noopCluster 满足 cluster.Cluster 其余方法，
// 额外暴露 DieChan() 能力，模拟 NatsCluster。
type fakeDieCluster struct {
	cluster.Cluster
	die chan struct{}
}

func (f *fakeDieCluster) DieChan() <-chan struct{} { return f.die }

func TestClusterDieChan_WithCapability(t *testing.T) {
	die := make(chan struct{}, 1)
	a := &Application{cls: &fakeDieCluster{Cluster: cluster.NewNoopCluster(), die: die}}
	if got := a.clusterDieChan(); got != (<-chan struct{})(die) {
		t.Fatal("clusterDieChan should return the cluster's die channel")
	}
}

func TestClusterDieChan_WithoutCapability(t *testing.T) {
	a := &Application{cls: cluster.NewNoopCluster()} // noop 不暴露 DieChan
	if a.clusterDieChan() != nil {
		t.Fatal("clusterDieChan should be nil for a cluster without DieChan capability")
	}
}

func TestAwaitDie_ReturnsOnClusterDie(t *testing.T) {
	die := make(chan struct{}, 1)
	a := &Application{
		cls:     &fakeDieCluster{Cluster: cluster.NewNoopCluster(), die: die},
		dieChan: make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		// sigChan=nil 永久阻塞信号路；dieChan 不关；仅靠集群 die 信号唤醒
		a.awaitDie(nil)
		close(done)
	}()
	die <- struct{}{}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("awaitDie did not return on cluster die signal")
	}
}

// fakeAgent 实现 agent.Agent，仅记录 Close 是否被调用。
type fakeAgent struct{ closed atomic.Bool }

func (f *fakeAgent) Session() *session.Session               { return nil }
func (f *fakeAgent) Push(uint32, []byte) error               { return nil }
func (f *fakeAgent) Response(uint32, uint32, []byte) error   { return nil }
func (f *fakeAgent) ResponseErr(uint32, uint32, int32) error { return nil }
func (f *fakeAgent) Close() error                            { f.closed.Store(true); return nil }
func (f *fakeAgent) RemoteAddr() net.Addr                    { return nil }
func (f *fakeAgent) IsAlive() bool                           { return false }
func (f *fakeAgent) OnClose(func(*session.Session))          {}

// 编译期断言 fakeAgent 满足 agent.Agent 接口
var _ agent.Agent = (*fakeAgent)(nil)

func TestStop_ClosesLiveAgents(t *testing.T) {
	a := New(WithSerializer("json", json.NewSerializer()))
	fa := &fakeAgent{}
	a.AgentMap().Store(1, fa)

	done := make(chan struct{})
	go func() { a.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return")
	}
	if !fa.closed.Load() {
		t.Fatal("Stop should close every live agent in AgentMap")
	}
}

// TestStop_NilAgentFacNoPanic 锁定 Stop() 中 `a.agentFac != nil` 守卫的契约：
// 字面量直构的 Application（不经 New，agentFac 为 nil，如 TestAwaitDie/TestClusterDieChan
// 的构造方式）调 Stop 不应 nil-panic。
func TestStop_NilAgentFacNoPanic(t *testing.T) {
	a := &Application{
		cls:             cluster.NewNoopCluster(),
		dieChan:         make(chan struct{}),
		shutdownTimeout: time.Second,
	}
	done := make(chan struct{})
	go func() { a.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return on nil-agentFac Application")
	}
}
