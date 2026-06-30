package internal

import (
	"context"

	matchpb "project/protocal/gen/match"
	"project/src/common/logger"
	"project/src/common/matchqueue"

	"google.golang.org/protobuf/proto"
)

// StartConsumer 启动 JetStream 消费：每条 MatchRequest 经 Submit 进主循环处理，
// 入队完成后才返回（即 ack，遵循 umbrella §5.3「入队即 ack」）。坏/空消息校验丢弃（ack），避免毒丸重投。
func (rt *Runtime) StartConsumer(ctx context.Context, mq matchqueue.MatchQueue) error {
	return mq.Consume(ctx, matchqueue.DurableMatchsvr, func(_ context.Context, data []byte) error {
		var req matchpb.MatchRequest
		if err := proto.Unmarshal(data, &req); err != nil {
			logger.Warn("match consume: bad payload", logger.Err(err))
			return nil // ack 丢弃
		}
		if req.Uid == 0 || req.ReqId == "" {
			logger.Warn("match consume: empty fields", logger.Int64("uid", req.Uid), logger.String("reqId", req.ReqId))
			return nil // ack 丢弃
		}
		done := make(chan struct{})
		rt.Submit(func() {
			rt.OnRequest(&req)
			close(done)
		})
		<-done // 入队完成后 ack
		return nil
	})
}
