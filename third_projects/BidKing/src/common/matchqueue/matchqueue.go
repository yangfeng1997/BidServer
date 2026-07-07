// Package matchqueue 提供匹配请求队列设施：router 侧发布、matchsvr 侧 durable 消费。
// 唯一真实现走 JetStream（at-least-once，匹配请求不丢）；单测用内存 fake。
// stream/subject/durable 名为包级常量，发布端与消费端共用同一真相源。
package matchqueue

import (
	"context"

	"google.golang.org/protobuf/proto"
)

const (
	StreamMatch         = "MATCH"         // JetStream stream 名
	SubjectMatchRequest = "match.request" // 匹配请求 subject
	DurableMatchsvr     = "matchsvr"      // matchsvr durable consumer 名（queue group）
)

// MatchQueue 匹配请求队列抽象。
type MatchQueue interface {
	// Publish 把 msg 序列化后发布到 subject。
	Publish(ctx context.Context, subject string, msg proto.Message) error
	// Consume 注册 durable consumer：每条消息回调 handler；handler 返回 nil → ack，非 nil → 不 ack 留重投。
	Consume(ctx context.Context, durable string, handler func(ctx context.Context, data []byte) error) error
	// Close 释放底层连接。
	Close() error
}
