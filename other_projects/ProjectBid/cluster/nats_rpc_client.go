package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	nats "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"projectbid/server/conn/message"
	"projectbid/server/errors"
	"projectbid/server/logger"
	"projectbid/server/route"
	"projectbid/server/session"
)

// NatsRPCClient 基于 NATS 的 RPC 客户端。
type NatsRPCClient struct {
	conn       *nats.Conn
	reqTimeout time.Duration
	serverID   string
}

// NatsRPCClientConfig NATS RPC 客户端配置。
type NatsRPCClientConfig struct {
	URL           string
	RequestTimeout time.Duration
	ServerID      string
}

// NewNatsRPCClient 创建 NATS RPC 客户端。
func NewNatsRPCClient(config NatsRPCClientConfig) (*NatsRPCClient, error) {
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 10 * time.Second
	}

	nc, err := nats.Connect(config.URL)
	if err != nil {
		return nil, fmt.Errorf("连接 NATS 失败: %w", err)
	}

	logger.Infow("已连接到 NATS", "URL", config.URL)

	return &NatsRPCClient{
		conn:       nc,
		reqTimeout: config.RequestTimeout,
		serverID:   config.ServerID,
	}, nil
}

// Call 发送 RPC 请求并等待响应。
func (c *NatsRPCClient) Call(ctx context.Context, rpcType RPCType, rt *route.Route, sess session.Session, msg *message.Message, target *Server) (*Response, error) {
	req := &Request{
		Type:       rpcType,
		FrontendId: c.serverID,
		Msg: &Msg{
			Route: rt.String(),
			Type:  MsgType(int32(msg.Type)),
			Data:  msg.Data,
			Id:    uint64(msg.ID),
		},
	}

	if sess != nil {
		sessionData, _ := json.Marshal(sess.GetData())
		req.Session = &SessionData{
			Id:   sess.ID(),
			Uid:  sess.UID(),
			Data: sessionData,
		}
	}
	req.Metadata = map[string]string{
		"source_server_id": c.serverID,
	}

	data, err := proto.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化 RPC 请求失败: %w", err)
	}

	subject := fmt.Sprintf("projectbid/rpc/%s/%s", target.Type, target.ID)
	respMsg, err := c.conn.RequestWithContext(ctx, subject, data)
	if err != nil {
		return nil, errors.NewError(fmt.Errorf("NATS RPC 调用失败: %w", err), errors.PIT408)
	}

	var resp Response
	if err := proto.Unmarshal(respMsg.Data, &resp); err != nil {
		return nil, errors.NewError(fmt.Errorf("反序列化 RPC 响应失败: %w", err), errors.PIT500)
	}

	return &resp, nil
}

// Send 发送单向消息（不等待响应）。
func (c *NatsRPCClient) Send(rt string, data []byte) error {
	req := &Msg{
		Route: rt,
		Data:  data,
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化消息失败: %w", err)
	}

	subject := fmt.Sprintf("projectbid/notify")
	if err := c.conn.Publish(subject, payload); err != nil {
		return fmt.Errorf("NATS 发布失败: %w", err)
	}

	return nil
}

// SendPush 向指定用户的前端服务器发送推送。
func (c *NatsRPCClient) SendPush(uid string, frontendSv *Server, push *PushMsg) error {
	payload, err := proto.Marshal(push)
	if err != nil {
		return fmt.Errorf("序列化推送消息失败: %w", err)
	}

	subject := fmt.Sprintf("projectbid/push/%s/%s", frontendSv.ID, uid)
	if err := c.conn.Publish(subject, payload); err != nil {
		return fmt.Errorf("NATS 推送失败: %w", err)
	}

	return nil
}

// SendKick 发送踢下线指令。
func (c *NatsRPCClient) SendKick(uid string, serverType string, kick *KickMsg) error {
	payload, err := proto.Marshal(kick)
	if err != nil {
		return fmt.Errorf("序列化踢下线消息失败: %w", err)
	}

	subject := fmt.Sprintf("projectbid/kick/%s/%s", serverType, uid)
	if err := c.conn.Publish(subject, payload); err != nil {
		return fmt.Errorf("NATS 踢下线失败: %w", err)
	}

	return nil
}

// Publish 向任意 NATS subject 发消息，用于远程 ResponseMID 回推到前端。
func (c *NatsRPCClient) Publish(subject string, data []byte) error {
	if err := c.conn.Publish(subject, data); err != nil {
		return fmt.Errorf("NATS 发布至 %s 失败: %w", subject, err)
	}
	return nil
}

// Stop 关闭客户端连接。
func (c *NatsRPCClient) Stop() error {
	c.conn.Close()
	return nil
}
