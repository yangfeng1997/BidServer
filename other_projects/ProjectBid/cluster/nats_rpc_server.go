package cluster

import (
	"fmt"

	nats "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"projectbid/server/errors"
	"projectbid/server/logger"
)

// NatsRPCServer 基于 NATS 的 RPC 服务端。
type NatsRPCServer struct {
	conn       *nats.Conn
	sub        *nats.Subscription
	pushSub    *nats.Subscription
	kickSub    *nats.Subscription
	serverID   string
	serverType string
	handler    RequestHandler
}

// NatsRPCServerConfig NATS RPC 服务端配置。
type NatsRPCServerConfig struct {
	URL        string
	ServerID   string
	ServerType string
}

// NewNatsRPCServer 创建 NATS RPC 服务端。
func NewNatsRPCServer(config NatsRPCServerConfig) (*NatsRPCServer, error) {
	nc, err := nats.Connect(config.URL)
	if err != nil {
		return nil, fmt.Errorf("连接 NATS 失败: %w", err)
	}

	logger.Infow("NATS RPC 服务端已连接", "URL", config.URL)

	return &NatsRPCServer{
		conn:       nc,
		serverID:   config.ServerID,
		serverType: config.ServerType,
	}, nil
}

// SetHandler 设置 RPC 请求处理器。
func (s *NatsRPCServer) SetHandler(handler RequestHandler) {
	s.handler = handler
}

// Listen 开始监听 RPC 请求。
func (s *NatsRPCServer) Listen() error {
	if s.handler == nil {
		return fmt.Errorf("未设置 RequestHandler")
	}

	// 监听 RPC 请求（按服务器类型和 ID）
	rpcSubject := fmt.Sprintf("projectbid/rpc/%s/%s", s.serverType, s.serverID)
	var err error
	s.sub, err = s.conn.Subscribe(rpcSubject, func(msg *nats.Msg) {
		var req Request
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			logger.Errorw("反序列化 RPC 请求失败", "错误", err)
			s.respondError(msg, fmt.Sprintf("反序列化失败: %s", err.Error()))
			return
		}

		resp, err := s.handler.HandleRequest(&req, msg.Reply)
		if err != nil {
			err = errors.NewError(err, errors.PIT500)
			logger.Errorw("处理 RPC 请求失败", "错误", err)
			s.respondError(msg, err.Error())
			return
		}

		respData, err := proto.Marshal(resp)
		if err != nil {
			logger.Errorw("序列化 RPC 响应失败", "错误", err)
			return
		}
		msg.Respond(respData)
	})
	if err != nil {
		return fmt.Errorf("订阅 RPC 主题失败: %w", err)
	}

	// 监听推送消息
	pushSubject := fmt.Sprintf("projectbid/push/%s/>", s.serverID)
	s.pushSub, err = s.conn.Subscribe(pushSubject, func(msg *nats.Msg) {
		var push PushMsg
		if err := proto.Unmarshal(msg.Data, &push); err != nil {
			logger.Errorw("反序列化推送消息失败", "错误", err)
			return
		}
		// 从 subject 提取 uid: projectbid/push/{serverID}/{uid}
		uid := extractUID(msg.Subject)
		if err := s.handler.HandlePush(&push, uid); err != nil {
			logger.Errorw("处理推送消息失败", "错误", err)
		}
	})
	if err != nil {
		return fmt.Errorf("订阅推送主题失败: %w", err)
	}

	// 监听踢下线消息
	kickSubject := fmt.Sprintf("projectbid/kick/%s/>", s.serverType)
	s.kickSub, err = s.conn.Subscribe(kickSubject, func(msg *nats.Msg) {
		var kick KickMsg
		if err := proto.Unmarshal(msg.Data, &kick); err != nil {
			logger.Errorw("反序列化踢下线消息失败", "错误", err)
			return
		}
		uid := extractUID(msg.Subject)
		if err := s.handler.HandleKick(&kick, uid); err != nil {
			logger.Errorw("处理踢下线消息失败", "错误", err)
		}
	})
	if err != nil {
		return fmt.Errorf("订阅踢下线主题失败: %w", err)
	}

	logger.Infow("NATS RPC 服务端开始监听",
		"服务器ID", s.serverID,
		"服务器类型", s.serverType,
	)

	return nil
}

// Stop 停止监听。
func (s *NatsRPCServer) Stop() error {
	if s.sub != nil {
		s.sub.Unsubscribe()
	}
	if s.pushSub != nil {
		s.pushSub.Unsubscribe()
	}
	if s.kickSub != nil {
		s.kickSub.Unsubscribe()
	}
	s.conn.Close()
	return nil
}

func (s *NatsRPCServer) respondError(msg *nats.Msg, errStr string) {
	resp := &Response{
		Error: &PBError{Code: errors.PIT500, Msg: errStr},
	}
	data, _ := proto.Marshal(resp)
	msg.Respond(data)
}

func extractUID(subject string) string {
	// subject 格式: projectbid/push/{serverID}/{uid}
	// 取最后一个 token
	for i := len(subject) - 1; i >= 0; i-- {
		if subject[i] == '/' {
			return subject[i+1:]
		}
	}
	return ""
}
