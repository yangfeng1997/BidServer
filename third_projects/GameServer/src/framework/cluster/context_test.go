package cluster

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type capturingReplier struct {
	data []byte
	err  error
	n    int
}

func (c *capturingReplier) Reply(data []byte, err error) { c.data, c.err, c.n = data, err, c.n+1 }

func TestReplierRoundTrip(t *testing.T) {
	r := &capturingReplier{}
	ctx := WithReplier(context.Background(), r)
	got := ReplierFromCtx(ctx)
	if got == nil {
		t.Fatal("ReplierFromCtx returned nil")
	}
	got.Reply([]byte("ok"), nil)
	if r.n != 1 || string(r.data) != "ok" {
		t.Fatalf("reply not delivered: n=%d data=%q", r.n, r.data)
	}
}

func TestReplierFromCtx_Absent(t *testing.T) {
	if ReplierFromCtx(context.Background()) != nil {
		t.Fatal("expected nil replier when absent")
	}
}

func TestErrDeferredReplyIdentity(t *testing.T) {
	wrapped := fmt.Errorf("outer: %w", ErrDeferredReply)
	if !errors.Is(wrapped, ErrDeferredReply) {
		t.Fatal("sentinel must survive wrapping")
	}
}
