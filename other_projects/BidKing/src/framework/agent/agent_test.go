package agent

import (
	"context"
	"errors"
	"net"
	"testing"

	"project/src/framework/network/message"
	"project/src/framework/network/packet"
	"project/src/framework/session"
)

// fakeConn 最小 ClientConn：仅 Close 被 connAgent.Close 调用，其余方法不触碰。
type fakeConn struct{ net.Conn }

func (fakeConn) Close() error                        { return nil }
func (fakeConn) ReadPacket() (*packet.Packet, error) { return nil, errors.New("not used") }

// newForwardTestAgent 构造可测 connAgent：填 handleData 转发分支 + Close 会触碰的字段。
func newForwardTestAgent(forward map[uint32]string, resp map[uint32]uint32,
	fn func(context.Context, *ForwardContext)) *connAgent {
	sm := session.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	return &connAgent{
		conn:           fakeConn{},
		session:        sm.New("127.0.0.1:1000"),
		sessions:       sm,
		chDie:          make(chan struct{}),
		ctx:            ctx,
		cancel:         cancel,
		forwardTable:   forward,
		respMsgIDTable: resp,
		forwardFn:      fn,
	}
}

func TestHandleData_ForwardsBoundRequest(t *testing.T) {
	var got *ForwardContext
	a := newForwardTestAgent(
		map[uint32]string{42: "lobbysvr"},
		map[uint32]uint32{42: 43},
		func(_ context.Context, fctx *ForwardContext) { got = fctx },
	)
	body, err := message.Encode(message.NewRequest(7, 42, []byte("payload")))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := a.handleData(body); err != nil {
		t.Fatalf("handleData: %v", err)
	}
	if got == nil {
		t.Fatal("forwardFn not called for bound msgID")
	}
	if got.Agent != a || got.ServerType != "lobbysvr" || got.MsgID != 42 || got.MID != 7 ||
		got.RespMsgID != 43 || got.MsgType != uint8(message.Request) ||
		string(got.Data) != "payload" {
		t.Fatalf("ForwardContext wrong: %+v", got)
	}
}

func TestHandleData_ForwardsBoundOneWay(t *testing.T) {
	var got *ForwardContext
	a := newForwardTestAgent(
		map[uint32]string{50: "roomsvr"},
		nil,
		func(_ context.Context, fctx *ForwardContext) { got = fctx },
	)
	body, err := message.Encode(message.NewOneWay(50, []byte("op")))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := a.handleData(body); err != nil {
		t.Fatalf("handleData: %v", err)
	}
	if got == nil || got.Agent != a || got.ServerType != "roomsvr" || got.MsgID != 50 ||
		got.MID != 0 || got.MsgType != uint8(message.OneWay) || string(got.Data) != "op" {
		t.Fatalf("ForwardContext wrong: %+v", got)
	}
}

func TestHandleData_UnboundMsgIDNoForward(t *testing.T) {
	called := false
	a := newForwardTestAgent(
		map[uint32]string{42: "lobbysvr"},
		nil,
		func(_ context.Context, _ *ForwardContext) { called = true },
	)
	body, err := message.Encode(message.NewRequest(1, 999, nil))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := a.handleData(body); err != nil {
		t.Fatalf("handleData: %v", err)
	}
	if called {
		t.Fatal("forwardFn must not be called for unbound msgID")
	}
}

func TestHandleData_CtxCanceledOnClose(t *testing.T) {
	var captured context.Context
	a := newForwardTestAgent(
		map[uint32]string{42: "lobbysvr"},
		map[uint32]uint32{42: 43},
		func(ctx context.Context, _ *ForwardContext) { captured = ctx },
	)
	body, err := message.Encode(message.NewRequest(7, 42, []byte("p")))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := a.handleData(body); err != nil {
		t.Fatalf("handleData: %v", err)
	}
	if captured == nil {
		t.Fatal("forwardFn not called")
	}
	if captured.Err() != nil {
		t.Fatalf("ctx should be live before Close, got %v", captured.Err())
	}
	_ = a.Close()
	if !errors.Is(captured.Err(), context.Canceled) {
		t.Fatalf("ctx should be canceled after Close, got %v", captured.Err())
	}
}
