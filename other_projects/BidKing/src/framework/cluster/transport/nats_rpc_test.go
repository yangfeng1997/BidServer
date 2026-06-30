package transport

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"project/src/framework/cluster"
	"project/src/framework/cluster/pb"
)

type fakePublisher struct {
	subj string
	data []byte
	n    int
}

func (f *fakePublisher) Publish(subj string, data []byte) error {
	f.subj, f.data, f.n = subj, data, f.n+1
	return nil
}

func TestPublishReply_Success(t *testing.T) {
	p := &fakePublisher{}
	publishReply(p, "ping.route", "inbox.1", []byte("body"), nil)
	if p.n != 1 || p.subj != "inbox.1" {
		t.Fatalf("publish not called correctly: %+v", p)
	}
	var resp pb.ClusterResponse
	if err := proto.Unmarshal(p.data, &resp); err != nil {
		t.Fatal(err)
	}
	if string(resp.Data) != "body" || resp.ErrMsg != "" {
		t.Fatalf("bad response: %+v", &resp)
	}
}

func TestPublishReply_Error(t *testing.T) {
	p := &fakePublisher{}
	publishReply(p, "ping.route", "inbox.1", nil, errors.New("boom"))
	var resp pb.ClusterResponse
	if err := proto.Unmarshal(p.data, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ErrMsg != "boom" || resp.Data != nil {
		t.Fatalf("expected error response, got %+v", &resp)
	}
}

func TestPublishReply_EmptyReplyNoOp(t *testing.T) {
	p := &fakePublisher{}
	publishReply(p, "ping.route", "", []byte("x"), nil)
	if p.n != 0 {
		t.Fatal("should not publish when reply subject empty")
	}
}

func TestCall_NilHandlerReturnsError(t *testing.T) {
	target := cluster.MakeNodeID(1, 1, 1)
	r := &NatsRPC{subject: target.Subject()} // 本地短路目标；handler 默认 nil
	_, err := r.Call(context.Background(), target, &pb.ClusterMessage{Route: "r", Data: []byte("x")})
	if !errors.Is(err, errHandlerNotSet) {
		t.Fatalf("expected errHandlerNotSet, got %v", err)
	}
}

func TestCallAsync_NilHandlerReturnsError(t *testing.T) {
	target := cluster.MakeNodeID(1, 1, 1)
	r := &NatsRPC{subject: target.Subject()}
	errCh := make(chan error, 1)
	r.CallAsync(context.Background(), target, &pb.ClusterMessage{Route: "r"}, func(_ []byte, err error) {
		errCh <- err
	})
	select {
	case err := <-errCh:
		if !errors.Is(err, errHandlerNotSet) {
			t.Fatalf("expected errHandlerNotSet, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("done callback not invoked")
	}
}

func TestHandleMessage_NilHandlerNoPanic(t *testing.T) {
	r := &NatsRPC{subject: "1.1.1"} // handler 默认 nil，conn 默认 nil
	body, err := proto.Marshal(&pb.ClusterMessage{Route: "r", Data: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	// Reply 为空（oneway）→ publishReply 提前返回不触碰 nil conn；
	// 修复前：r.handler 为 nil → nil-deref panic；修复后：守卫提前返回，无 panic。
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("handleMessage panicked with nil handler: %v", rec)
		}
	}()
	r.handleMessage(&nats.Msg{Reply: "", Data: body})
}
