package rpc

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/emptypb"

	"project/internal/core/errcode"
)

func TestDispatcherDispatch(t *testing.T) {
	d := NewDispatcher()
	called := false
	if err := d.Register("Test/Ping", func(ctx Ctx, body []byte, reply func([]byte, error)) error {
		called = true
		if string(body) != "ping" {
			t.Fatalf("body=%q, want ping", body)
		}
		if ctx.FromNodeID() != 42 {
			t.Fatalf("from node=%d, want 42", ctx.FromNodeID())
		}
		reply([]byte("pong"), nil)
		return nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	var got []byte
	err := d.Dispatch("Test/Ping", Background().WithFromNode(42), []byte("ping"), func(payload []byte, err error) {
		if err != nil {
			t.Fatalf("reply err: %v", err)
		}
		got = payload
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !called {
		t.Fatal("handler was not called")
	}
	if string(got) != "pong" {
		t.Fatalf("reply=%q, want pong", got)
	}
}

func TestDispatcherRegisterRejectsDuplicateRoute(t *testing.T) {
	d := NewDispatcher()
	h := func(Ctx, []byte, func([]byte, error)) error { return nil }
	if err := d.Register("Test/Dup", h); err != nil {
		t.Fatalf("Register first: %v", err)
	}
	if err := d.Register("Test/Dup", h); errcode.CodeOf(err) != errcode.ERR_INTERNAL {
		t.Fatalf("code=%d, want ERR_INTERNAL, err=%v", errcode.CodeOf(err), err)
	}
}

func TestDispatcherMustRegisterPanicsOnDuplicateRoute(t *testing.T) {
	d := NewDispatcher()
	h := func(Ctx, []byte, func([]byte, error)) error { return nil }
	d.MustRegister("Test/Dup", h)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	d.MustRegister("Test/Dup", h)
}

func TestDispatcherMissingRoute(t *testing.T) {
	err := NewDispatcher().Dispatch("Missing/Route", Background(), nil, nil)
	if errcode.CodeOf(err) != errcode.ERR_NO_ROUTE {
		t.Fatalf("code=%d, want ERR_NO_ROUTE, err=%v", errcode.CodeOf(err), err)
	}
}

func TestDispatcherReturnsHandlerError(t *testing.T) {
	want := errors.New("boom")
	d := NewDispatcher()
	d.MustRegister("Test/Err", func(Ctx, []byte, func([]byte, error)) error { return want })
	if err := d.Dispatch("Test/Err", Background(), nil, nil); !errors.Is(err, want) {
		t.Fatalf("err=%v, want %v", err, want)
	}
}

func TestDispatcherRecoverRoute(t *testing.T) {
	d := NewDispatcher()
	d.MustRegister("Test/Panic", RecoverRoute("Test/Panic", func(Ctx, []byte, func([]byte, error)) error {
		panic("boom")
	}))
	err := d.Dispatch("Test/Panic", Background(), nil, nil)
	if errcode.CodeOf(err) != errcode.ERR_INTERNAL {
		t.Fatalf("code=%d, want ERR_INTERNAL, err=%v", errcode.CodeOf(err), err)
	}
}

func TestOnceReply(t *testing.T) {
	calls := 0
	reply := OnceReply(func([]byte, error) { calls++ })
	reply(nil, nil)
	reply(nil, nil)
	if calls != 1 {
		t.Fatalf("calls=%d, want 1", calls)
	}
}

func TestReplyProto(t *testing.T) {
	var got []byte
	reply := ReplyProto[*emptypb.Empty](func(payload []byte, err error) {
		if err != nil {
			t.Fatalf("reply err: %v", err)
		}
		got = payload
	})
	reply(&emptypb.Empty{}, nil)
	if got == nil {
		t.Fatal("expected marshaled payload")
	}
}
