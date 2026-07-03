package gate

import (
	"path/filepath"
	"testing"

	"project/internal/server/routeragent"
)

type posterFunc func(func())

func (f posterFunc) Post(fn func()) { f(fn) }

func TestRagentClientConnectsToRouterAgentUDS(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "ra.sock")
	ra := routeragent.NewModuleForTest(func(fn func()) { fn() })
	ra.ApplyConfig(sock, "", 0)
	if err := ra.AfterInit(); err != nil {
		t.Fatalf("start routeragent: %v", err)
	}
	defer ra.BeforeShutdown()

	client := NewRagentClient(0x01010101, sock, posterFunc(func(fn func()) { fn() }), nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect routeragent: %v", err)
	}
	defer client.Close()

	if _, ok := ra.MemberTable().GetByNodeID(0x01010101); !ok {
		t.Fatal("routeragent should register gate node after handshake")
	}
}
