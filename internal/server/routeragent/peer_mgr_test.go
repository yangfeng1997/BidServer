package routeragent

import "testing"

func TestPeerMgrNew(t *testing.T) {
	m := NewPeerMgr()
	if m == nil {
		t.Fatal("NewPeerMgr returned nil")
	}
}

func TestPeerMgrGetMissing(t *testing.T) {
	m := NewPeerMgr()
	if p := m.Get("nonexistent"); p != nil {
		t.Error("Get should return nil for missing peer")
	}
}

func TestPeerMgrDisconnect(t *testing.T) {
	m := NewPeerMgr()
	m.SetState("addr:1", PeerConnecting)
	m.Disconnect("addr:1")
	if p := m.Get("addr:1"); p != nil {
		t.Error("peer should be gone after Disconnect")
	}
}
