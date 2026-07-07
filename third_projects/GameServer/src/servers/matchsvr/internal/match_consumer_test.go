package internal

import (
	"context"
	"testing"

	matchpb "project/protocal/gen/match"
	"project/src/common/matchqueue"
)

func TestStartConsumer_EnqueuesAndDedups(t *testing.T) {
	rt := newTestMatchRuntime(t) // MatchSize=2 → 单条不成桌，留队可观察
	defer rt.Stop()
	mq := matchqueue.NewMemoryQueue()
	if err := rt.StartConsumer(context.Background(), mq); err != nil {
		t.Fatalf("start consumer: %v", err)
	}

	_ = mq.Publish(context.Background(), matchqueue.SubjectMatchRequest,
		&matchpb.MatchRequest{Uid: 1, ReqId: "r1", Mmr: 1000, LobbyNodeId: "1.2.1"})
	matchRunOnLoop(t, rt, func() {
		if rt.queueLen() != 1 {
			t.Fatalf("consumed request should be enqueued, got %d", rt.queueLen())
		}
	})

	// 重投同一条 → (uid,reqId) 去重，不重复入队
	if err := mq.Redeliver(context.Background(), 0); err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	matchRunOnLoop(t, rt, func() {
		if rt.queueLen() != 1 {
			t.Fatalf("redelivery should dedup, got %d", rt.queueLen())
		}
	})
}

func TestStartConsumer_EmptyFieldsDropped(t *testing.T) {
	rt := newTestMatchRuntime(t)
	defer rt.Stop()
	mq := matchqueue.NewMemoryQueue()
	if err := rt.StartConsumer(context.Background(), mq); err != nil {
		t.Fatalf("start consumer: %v", err)
	}
	// 空字段消息（uid=0/reqId="")应被消费侧校验丢弃（返回 nil 即 ack），不入队。
	if err := mq.Publish(context.Background(), matchqueue.SubjectMatchRequest, &matchpb.MatchRequest{Uid: 0}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	matchRunOnLoop(t, rt, func() {
		if rt.queueLen() != 0 {
			t.Fatalf("empty/invalid request should not enqueue, got %d", rt.queueLen())
		}
	})
}
