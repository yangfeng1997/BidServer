package service

import (
	"context"
	"encoding/json"
	"net"

	"google.golang.org/protobuf/proto"

	"projectbid/server/cluster"
	"projectbid/server/conn/message"
	"projectbid/server/constants"
	"projectbid/server/discovery"
	"projectbid/server/errors"
	"projectbid/server/pipeline"
	"projectbid/server/route"
	"projectbid/server/serialize"
	"projectbid/server/session"
)

// RemoteService 将 NATS RPC 请求桥接到本地 HandlerPool。
type RemoteService struct {
	handlerPool      *HandlerPool
	serializer       serialize.Serializer
	handlerHooks     *pipeline.HandlerHooks
	sessionPool      session.SessionPool
	rpcClient        cluster.RPCClient
	serviceDiscovery discovery.ServiceDiscovery
}

// NewRemoteService 创建远程 RPC 请求处理器。
func NewRemoteService(
	pool *HandlerPool,
	ser serialize.Serializer,
	hooks *pipeline.HandlerHooks,
	sp session.SessionPool,
	rpcClient cluster.RPCClient,
	sd discovery.ServiceDiscovery,
) *RemoteService {
	return &RemoteService{
		handlerPool:      pool,
		serializer:       ser,
		handlerHooks:     hooks,
		sessionPool:      sp,
		rpcClient:        rpcClient,
		serviceDiscovery: sd,
	}
}

// HandleRequest 处理远程 RPC 请求。
func (h *RemoteService) HandleRequest(req *cluster.Request, replySubject string) (*cluster.Response, error) {
	rt, err := route.Decode(req.Msg.Route)
	if err != nil {
		wrappedErr := errors.NewError(err, errors.PIT400)
		return &cluster.Response{Error: &cluster.PBError{Code: errors.PIT400, Msg: wrappedErr.Error()}}, wrappedErr
	}

	ctx := context.WithValue(context.Background(), constants.RouteKey, req.Msg.Route)
	ctx = context.WithValue(ctx, constants.RequestIDKey, req.Msg.Id)

	// 如果有远程 session 数据，注入上下文
	var sess session.Session
	if req.Session != nil {
		sess = h.createRemoteSession(req, replySubject)
		ctx = context.WithValue(ctx, constants.SessionCtxKey, sess)
	}

	msgType := message.Type(req.Msg.Type)

	data, err := h.handlerPool.ProcessHandlerMessage(ctx, rt, h.serializer, h.handlerHooks, sess, req.Msg.Data, msgType)
	if err != nil {
		wrappedErr := errors.NewError(err, errors.PIT500)
		return &cluster.Response{Error: &cluster.PBError{Code: errors.PIT500, Msg: wrappedErr.Error()}}, nil
	}

	return &cluster.Response{Data: data}, nil
}

// HandlePush 处理远程推送（将推送转发给本地已连接的用户会话）。
func (h *RemoteService) HandlePush(push *cluster.PushMsg, uid string) error {
	s := h.sessionPool.GetSessionByUID(uid)
	if s == nil {
		return nil // 用户不在本服务器上
	}
	return s.Push(push.Route, push.Data)
}

// HandleKick 处理远程踢下线（踢出本服务器上的指定用户）。
func (h *RemoteService) HandleKick(kick *cluster.KickMsg, uid string) error {
	s := h.sessionPool.GetSessionByUID(uid)
	if s == nil {
		return nil // 用户不在本服务器上
	}
	return s.Kick(context.Background())
}

// createRemoteSession 从跨服 session 数据创建本地 session 对象。
func (h *RemoteService) createRemoteSession(req *cluster.Request, replySubject string) session.Session {
	entity := &remoteEntity{
		serializer:       h.serializer,
		rpcClient:        h.rpcClient,
		serviceDiscovery: h.serviceDiscovery,
		frontendID:       req.FrontendId,
		natsReply:        replySubject,
	}
	s := h.sessionPool.NewSession(entity)
	_ = s.Bind(context.Background(), req.Session.Uid)
	// 恢复 session 元数据
	if len(req.Session.Data) > 0 {
		var sessionData map[string]interface{}
		if err := json.Unmarshal(req.Session.Data, &sessionData); err == nil {
			for k, v := range sessionData {
				switch val := v.(type) {
				case float64:
					_ = s.Set(k, val)
				case string:
					_ = s.Set(k, val)
				default:
					jb, _ := json.Marshal(v)
					_ = s.Set(k, string(jb))
				}
			}
		}
	}
	return s
}

// remoteEntity 在后端服务器上实现 networkentity.NetworkEntity，
// 通过 NATS 将 Push/ResponseMID 转发到前端服务器。
type remoteEntity struct {
	serializer       serialize.Serializer
	rpcClient        cluster.RPCClient
	serviceDiscovery discovery.ServiceDiscovery
	frontendID       string
	natsReply        string
}

func (e *remoteEntity) Push(route string, v interface{}) error {
	payload, err := serializeOrRawBytes(e.serializer, v)
	if err != nil {
		return err
	}

	frontSv, err := e.serviceDiscovery.GetServer(e.frontendID)
	if err != nil {
		return err
	}

	push := &cluster.PushMsg{
		Route: route,
		Data:  payload,
	}
	return e.rpcClient.SendPush("", frontSv, push)
}

func (e *remoteEntity) ResponseMID(ctx context.Context, mid uint, v interface{}, isError ...bool) error {
	if e.natsReply == "" {
		return nil
	}
	payload, err := serializeOrRawBytes(e.serializer, v)
	if err != nil {
		return err
	}
	resp := &cluster.Response{Data: payload}
	data, err := proto.Marshal(resp)
	if err != nil {
		return err
	}
	return e.rpcClient.Publish(e.natsReply, data)
}

func (e *remoteEntity) Close() error       { return nil }
func (e *remoteEntity) RemoteAddr() net.Addr { return nil }

func serializeOrRawBytes(s serialize.Serializer, v interface{}) ([]byte, error) {
	if d, ok := v.([]byte); ok {
		return d, nil
	}
	return s.Marshal(v)
}
