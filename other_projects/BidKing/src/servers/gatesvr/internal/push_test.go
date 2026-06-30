package internal

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	gatepb "project/protocal/gen/gate"
	"project/src/framework/agent"
	"project/src/framework/session"
)

// fakePushAgent 嵌入 agent.Agent，仅覆写 Push 以捕获推送。
type fakePushAgent struct {
	agent.Agent
	lastPushMsgID uint32
	lastPushBody  []byte
}

func (a *fakePushAgent) Push(msgID uint32, data []byte) error {
	a.lastPushMsgID = msgID
	a.lastPushBody = data
	return nil
}

func TestPushToClient_PushesByUID(t *testing.T) {
	sm := session.NewManager()
	agents := agent.NewMap()
	m := NewGateModule("1.1.1", sm, &kickFakeCluster{}, agents)
	h := NewGateHandler(m)

	uid := int64(10001)
	s := sm.New("127.0.0.1:1234")
	_ = sm.Bind(context.Background(), s, uid)
	ag := &fakePushAgent{}
	agents.Store(s.ID(), ag)

	env := &gatepb.RPC_PushToClient{Uid: uid, MsgId: 2031, Body: []byte(`{"uid":5,"online":true}`)}
	raw, _ := proto.Marshal(env)
	h.PushToClient(context.Background(), raw)

	if ag.lastPushMsgID != 2031 || string(ag.lastPushBody) != `{"uid":5,"online":true}` {
		t.Fatalf("push: %d %s", ag.lastPushMsgID, ag.lastPushBody)
	}
}
