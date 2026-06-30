// Package cluster 提供服务间 RPC 通信接口与 NATS 实现。
package cluster

import (
	"context"
	"encoding/json"

	"projectbid/server/conn/message"
	"projectbid/server/protos"
	"projectbid/server/route"
	"projectbid/server/session"
)

// Proto 类型别名，保持包外 API 兼容。
type (
	RPCType  = protos.RPCType
	Response = protos.Response
	Msg      = protos.Msg
	Request  = protos.Request
	SessionData = protos.Session
	PushMsg  = protos.Push
	KickMsg  = protos.KickMsg
	MsgType  = protos.MsgType
	PBError  = protos.Error
)

// Proto 枚举值重导出。
const (
	RPCTypeSys  = protos.RPCType_RPC_TYPE_SYS
	RPCTypeUser = protos.RPCType_RPC_TYPE_USER
	MsgTypeRequest = protos.MsgType_MSG_TYPE_REQUEST
	MsgTypeNotify  = protos.MsgType_MSG_TYPE_NOTIFY
)

// RPCClient 定义服务间调用的客户端接口。
type RPCClient interface {
	// Call 发送 RPC 请求并等待响应。
	Call(ctx context.Context, rpcType RPCType, rt *route.Route, sess session.Session, msg *message.Message, target *Server) (*Response, error)
	// Send 发送单向 RPC 消息（不等待响应）。
	Send(rt string, data []byte) error
	// Publish 向任意 NATS subject 发布消息。
	Publish(subject string, data []byte) error
	// SendPush 向指定用户推送消息。
	SendPush(uid string, frontendSv *Server, push *PushMsg) error
	// SendKick 踢出指定用户。
	SendKick(uid string, serverType string, kick *KickMsg) error
	// Stop 关闭客户端连接。
	Stop() error
}

// RPCServer 定义服务间调用的服务端接口。
type RPCServer interface {
	// Listen 开始监听 RPC 请求。
	Listen() error
	// Stop 停止监听。
	Stop() error
}

// RequestHandler 处理 RPC 请求的回调。
type RequestHandler interface {
	HandleRequest(req *Request, replySubject string) (*Response, error)
	HandlePush(push *PushMsg, uid string) error
	HandleKick(kick *KickMsg, uid string) error
}

// Server 集群中的服务器信息（对齐 Pitaya）。
type Server struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Metadata map[string]string `json:"metadata"`
	Frontend bool              `json:"frontend"`
	Hostname string            `json:"hostname"`
}

// NewServer 创建服务器信息。
func NewServer(id, svType string, frontend bool, metadata map[string]string) *Server {
	if metadata == nil {
		metadata = make(map[string]string)
	}
	return &Server{
		ID:       id,
		Type:     svType,
		Metadata: metadata,
		Frontend: frontend,
	}
}

// AsJSON 返回 JSON 字符串表示。
func (s *Server) AsJSON() string {
	data, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return string(data)
}
