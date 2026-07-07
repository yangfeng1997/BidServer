package matchqueue

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
)

// JetStreamQueue 真实 JetStream 适配器。沙箱不实跑，仅编译验证；实跑需 NATS+JetStream 环境。
type JetStreamQueue struct {
	nc *nats.Conn
	js jetstream.JetStream
}

// NewJetStreamQueue 连接 NATS 并建 JetStream 句柄。
func NewJetStreamQueue(urls []string) (*JetStreamQueue, error) {
	url := nats.DefaultURL
	if len(urls) > 0 {
		url = urls[0]
	}
	nc, err := nats.Connect(url, nats.MaxReconnects(-1), nats.ReconnectWait(time.Second))
	if err != nil {
		return nil, fmt.Errorf("matchqueue connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("matchqueue jetstream: %w", err)
	}
	return &JetStreamQueue{nc: nc, js: js}, nil
}

// ensureStream 幂等建/确保 MATCH stream（WorkQueue：消费后删，匹配请求一次性）。
func (q *JetStreamQueue) ensureStream(ctx context.Context) error {
	_, err := q.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      StreamMatch,
		Subjects:  []string{SubjectMatchRequest},
		Retention: jetstream.WorkQueuePolicy,
	})
	return err
}

// Publish 把 msg 序列化后发布到 subject。
func (q *JetStreamQueue) Publish(ctx context.Context, subject string, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	if err := q.ensureStream(ctx); err != nil {
		return err
	}
	_, err = q.js.Publish(ctx, subject, data)
	return err
}

// Consume 注册 durable consumer：每条消息回调 handler；handler 返回 nil → ack，非 nil → 不 ack 留重投。
func (q *JetStreamQueue) Consume(ctx context.Context, durable string, handler func(context.Context, []byte) error) error {
	if err := q.ensureStream(ctx); err != nil {
		return err
	}
	cons, err := q.js.CreateOrUpdateConsumer(ctx, StreamMatch, jetstream.ConsumerConfig{
		Durable:   durable,
		AckPolicy: jetstream.AckExplicitPolicy,
	})
	if err != nil {
		return err
	}
	_, err = cons.Consume(func(m jetstream.Msg) {
		if herr := handler(ctx, m.Data()); herr != nil {
			return // 不 ack，留重投
		}
		_ = m.Ack()
	})
	return err
}

// Close 释放底层连接。
func (q *JetStreamQueue) Close() error {
	if q.nc != nil {
		q.nc.Close()
	}
	return nil
}

var _ MatchQueue = (*JetStreamQueue)(nil)
