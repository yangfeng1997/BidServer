package matchqueue

import (
	"context"
	"testing"

	matchpb "project/protocal/gen/match"
	"google.golang.org/protobuf/proto"
)

func TestMemoryQueue_PublishConsumeAck(t *testing.T) {
	q := NewMemoryQueue()
	var got []*matchpb.MatchRequest
	if err := q.Consume(context.Background(), DurableMatchsvr, func(_ context.Context, data []byte) error {
		var req matchpb.MatchRequest
		if err := proto.Unmarshal(data, &req); err != nil {
			return err
		}
		got = append(got, &req)
		return nil // ack
	}); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if err := q.Publish(context.Background(), SubjectMatchRequest, &matchpb.MatchRequest{Uid: 7, ReqId: "r1", Mmr: 1000}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(got) != 1 || got[0].Uid != 7 || got[0].ReqId != "r1" {
		t.Fatalf("want 1 msg uid=7 r1, got %+v", got)
	}
	if n := len(q.Published()); n != 1 {
		t.Fatalf("want 1 published, got %d", n)
	}
}

func TestMemoryQueue_Redeliver(t *testing.T) {
	q := NewMemoryQueue()
	calls := 0
	_ = q.Consume(context.Background(), DurableMatchsvr, func(_ context.Context, _ []byte) error {
		calls++
		return nil
	})
	_ = q.Publish(context.Background(), SubjectMatchRequest, &matchpb.MatchRequest{Uid: 1, ReqId: "r1"})
	if err := q.Redeliver(context.Background(), 0); err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	if calls != 2 {
		t.Fatalf("want 2 handler calls (publish + redeliver), got %d", calls)
	}
}
