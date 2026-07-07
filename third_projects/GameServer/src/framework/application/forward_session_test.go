package application

import (
	"testing"

	clusterpb "project/src/framework/cluster/pb"
)

func TestNewForwardSession_FillsUid(t *testing.T) {
	s := newForwardSession(nil, 42, 1, "1.1.1", 10001)
	if s.Uid != 10001 {
		t.Fatalf("uid not filled: %d", s.Uid)
	}
	if s.ClientMid != 42 || s.MsgType != 1 || s.FrontendId != "1.1.1" {
		t.Fatalf("base fields wrong: %+v", s)
	}
}

func TestNewForwardSession_KeepsExistingFrontend(t *testing.T) {
	base := &clusterpb.ClusterSession{FrontendId: "9.9.9"}
	s := newForwardSession(base, 1, 3, "1.1.1", 7)
	if s.FrontendId != "9.9.9" || s.Uid != 7 {
		t.Fatalf("unexpected: %+v", s)
	}
}
