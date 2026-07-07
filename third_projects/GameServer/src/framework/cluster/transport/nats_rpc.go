package transport

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"project/src/common/logger"
	"project/src/framework/cluster"
	"project/src/framework/cluster/pb"
)

const defaultCallTimeout = 5 * time.Second

// errHandlerNotSet 在 handler 注入前被解引用时返回，把潜在 nil-deref panic
// （会在分离的 NATS 回调 goroutine 中崩整进程）降级为干净错误（防御性）。
var errHandlerNotSet = errors.New("cluster: handler not set")

// NatsRPC 基于 NATS 的集群 RPC，每个进程一个 subject
type NatsRPC struct {
	conn          *nats.Conn
	subject       string
	handler       cluster.MessageHandler
	sub           *nats.Subscription
	dieCh         chan struct{}
	asyncDispatch bool // 入站消息每条独立 goroutine 处理（仅无状态的 router 开启）
}

// NewNatsRPC 创建 NatsRPC，handler 由 Application.Start() 通过 SetHandler 注入
func NewNatsRPC(urls []string, self cluster.NodeID, dieCh chan struct{}, asyncDispatch bool) (*NatsRPC, error) {
	r := &NatsRPC{
		subject:       self.Subject(),
		dieCh:         dieCh,
		asyncDispatch: asyncDispatch,
	}
	opts := []nats.Option{
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			logger.Warn("nats disconnected", logger.Err(err))
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			logger.Info("nats reconnected", logger.String("url", nc.ConnectedUrl()))
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			if nc.LastError() != nil {
				logger.Error("nats connection permanently closed", logger.Err(nc.LastError()))
				select {
				case dieCh <- struct{}{}:
				default:
				}
			}
		}),
	}
	url := nats.DefaultURL
	if len(urls) > 0 {
		url = urls[0]
	}
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats connect failed: %w", err)
	}
	r.conn = nc
	return r, nil
}

// SetHandler 由 Application.Start() 自动注入
func (r *NatsRPC) SetHandler(h cluster.MessageHandler) { r.handler = h }

// Start 订阅本节点 subject
func (r *NatsRPC) Start() error {
	sub, err := r.conn.Subscribe(r.subject, r.onMessage)
	if err != nil {
		return fmt.Errorf("nats subscribe failed: %w", err)
	}
	r.sub = sub
	logger.Info("nats rpc listening", logger.String("subject", r.subject))
	return nil
}

// Stop 取消订阅，drain 连接
func (r *NatsRPC) Stop() {
	if r.sub != nil {
		r.sub.Unsubscribe()
	}
	r.conn.Drain()
}

// Send 单向发送，不等返回
func (r *NatsRPC) Send(ctx context.Context, target cluster.NodeID, msg *pb.ClusterMessage) error {
	r.fillMeta(ctx, msg)
	body, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}
	return r.conn.Publish(target.Subject(), body)
}

// Call 同步有返回 RPC
func (r *NatsRPC) Call(ctx context.Context, target cluster.NodeID, msg *pb.ClusterMessage) ([]byte, error) {
	r.fillMeta(ctx, msg)

	// 本地短路
	if target.Subject() == r.subject {
		if r.handler == nil {
			return nil, errHandlerNotSet
		}
		return r.handler(ctx, msg.Data, msg.Route)
	}

	ctx, cancel := r.ensureDeadline(ctx, msg)
	defer cancel()
	body, err := proto.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal failed: %w", err)
	}
	reply, err := r.conn.RequestWithContext(ctx, target.Subject(), body)
	if err != nil {
		return nil, fmt.Errorf("nats call failed: %w", err)
	}
	return r.parseResponse(reply.Data)
}

// CallAsync 异步有返回 RPC，done 在 NATS goroutine 执行
func (r *NatsRPC) CallAsync(ctx context.Context, target cluster.NodeID, msg *pb.ClusterMessage, done func([]byte, error)) {
	r.fillMeta(ctx, msg)

	// 本地短路
	if target.Subject() == r.subject {
		if r.handler == nil {
			done(nil, errHandlerNotSet)
			return
		}
		go func() { done(r.handler(ctx, msg.Data, msg.Route)) }()
		return
	}

	ctx, cancel := r.ensureDeadline(ctx, msg)
	body, err := proto.Marshal(msg)
	if err != nil {
		cancel()
		done(nil, fmt.Errorf("marshal failed: %w", err))
		return
	}
	go func() {
		defer cancel()
		r.doRequest(ctx, target.Subject(), body, done)
	}()
}

func (r *NatsRPC) doRequest(ctx context.Context, subject string, body []byte, done func([]byte, error)) {
	reply, err := r.conn.RequestWithContext(ctx, subject, body)
	if err != nil {
		done(nil, fmt.Errorf("nats call failed: %w", err))
		return
	}
	done(r.parseResponse(reply.Data))
}

// onMessage 订阅回调：异步节点（router）每条消息独立 goroutine 处理，
// 订阅 goroutine 立即返回取下一条，转发互不阻塞；其余节点保持顺序处理，
// 保住"同目标有序"与帧驱动语义。
func (r *NatsRPC) onMessage(natsMsg *nats.Msg) {
	if r.asyncDispatch {
		go r.handleMessage(natsMsg)
		return
	}
	r.handleMessage(natsMsg)
}

// publisher 抽象 NATS 发布，便于 publishReply 单测（*nats.Conn 满足之）
type publisher interface {
	Publish(subj string, data []byte) error
}

// natsReplier 主循环异步回包：持 route + reply 主题，延迟到 continuation 再发包
type natsReplier struct {
	conn  publisher
	route string
	reply string
}

func (n *natsReplier) Reply(data []byte, err error) {
	publishReply(n.conn, n.route, n.reply, data, err)
}

// publishReply 封装 ClusterResponse 并发布到 reply 主题；reply 为空（OneWay）时 no-op
func publishReply(p publisher, route, reply string, data []byte, err error) {
	if reply == "" {
		if err != nil {
			logger.Warn("cluster: handler error (oneway)", logger.String("route", route), logger.Err(err))
		}
		return
	}
	resp := &pb.ClusterResponse{Data: data}
	if err != nil {
		resp.Data = nil
		resp.ErrMsg = err.Error()
	}
	body, merr := proto.Marshal(resp)
	if merr != nil {
		logger.Warn("cluster: marshal response failed", logger.Err(merr))
		return
	}
	if perr := p.Publish(reply, body); perr != nil {
		logger.Warn("cluster: reply failed", logger.Err(perr))
	}
}

// handleMessage 解信封 → 重建 ctx → 调 handler → 回包（原 onMessage 函数体，逻辑不变）
func (r *NatsRPC) handleMessage(natsMsg *nats.Msg) {
	var cm pb.ClusterMessage
	if err := proto.Unmarshal(natsMsg.Data, &cm); err != nil {
		logger.Warn("cluster: unmarshal failed", logger.Err(err))
		return
	}

	// 重建 ctx：注入 session、trace_id、deadline（超时传播）
	ctx := context.Background()
	if cm.Session != nil {
		ctx = cluster.WithSession(ctx, cm.Session)
	}
	if cm.TraceId != "" {
		ctx = cluster.WithTraceID(ctx, cm.TraceId)
	}
	if cm.Deadline > 0 {
		deadline := time.Unix(0, cm.Deadline)
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}

	ctx = cluster.WithReplier(ctx, &natsReplier{conn: r.conn, route: cm.Route, reply: natsMsg.Reply})

	if r.handler == nil {
		logger.Warn("cluster: message received before handler set", logger.String("route", cm.Route))
		publishReply(r.conn, cm.Route, natsMsg.Reply, nil, errHandlerNotSet)
		return
	}

	data, err := r.handler(ctx, cm.Data, cm.Route)
	if errors.Is(err, cluster.ErrDeferredReply) {
		return // handler 将经 Replier 异步回包
	}
	publishReply(r.conn, cm.Route, natsMsg.Reply, data, err)
}

// fillMeta 从 ctx 提取 from/trace_id/deadline 填入 ClusterMessage
func (r *NatsRPC) fillMeta(ctx context.Context, msg *pb.ClusterMessage) {
	msg.From = r.subject
	if traceID := cluster.TraceIDFromCtx(ctx); traceID != "" {
		msg.TraceId = traceID
	}
	if deadline, ok := ctx.Deadline(); ok {
		msg.Deadline = deadline.UnixNano()
	}
}

// ensureDeadline 若 ctx 无 deadline 则加默认超时，同时更新 msg.Deadline。
// 返回的 cancel 必须在请求结束后调用（无论是否新建超时都安全），避免 context 泄漏。
func (r *NatsRPC) ensureDeadline(ctx context.Context, msg *pb.ClusterMessage) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); !ok {
		ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
		deadline, _ := ctx.Deadline()
		msg.Deadline = deadline.UnixNano()
		return ctx, cancel
	}
	return ctx, func() {}
}

// parseResponse 解析 protobuf ClusterResponse
func (r *NatsRPC) parseResponse(data []byte) ([]byte, error) {
	var resp pb.ClusterResponse
	if err := proto.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response failed: %w", err)
	}
	if resp.ErrMsg != "" {
		return nil, errors.New(resp.ErrMsg)
	}
	return resp.Data, nil
}
