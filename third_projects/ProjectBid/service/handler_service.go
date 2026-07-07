package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	"projectbid/server/agent"
	"projectbid/server/component"
	"projectbid/server/cluster"
	"projectbid/server/conn/codec"
	"projectbid/server/conn/message"
	"projectbid/server/conn/packet"
	"projectbid/server/acceptor"
	"projectbid/server/constants"
	"projectbid/server/logger"
	"projectbid/server/pipeline"
	"projectbid/server/route"
	"projectbid/server/serialize"
	"projectbid/server/session"
)

// HandlerService 负责从连接读取消息、解码、路由分发与反射调用。
type HandlerService struct {
	handlerHooks   *pipeline.HandlerHooks
	chLocalProcess chan unhandledMessage
	decoder        codec.PacketDecoder
	serializer     serialize.Serializer
	agentFactory   agent.AgentFactory
	handlerPool    *HandlerPool
	services       map[string]*Service
	serverType     string
	natsClient     *cluster.NatsRPCClient
}

type unhandledMessage struct {
	ctx   context.Context
	agt   agent.Agent
	route *route.Route
	msg   *message.Message
}

// NewHandlerService 创建 HandlerService 实例。
func NewHandlerService(
	packetDecoder codec.PacketDecoder,
	serializer serialize.Serializer,
	localProcessBufferSize int,
	agentFactory agent.AgentFactory,
	handlerHooks *pipeline.HandlerHooks,
	handlerPool *HandlerPool,
	serverType string,
) *HandlerService {
	return &HandlerService{
		handlerHooks:   handlerHooks,
		chLocalProcess: make(chan unhandledMessage, localProcessBufferSize),
		decoder:        packetDecoder,
		serializer:     serializer,
		agentFactory:   agentFactory,
		handlerPool:    handlerPool,
		services:       make(map[string]*Service),
		serverType:     serverType,
	}
}

// Register 注册一个组件，提取其 handler 方法并加入池中。
func (h *HandlerService) Register(comp component.Component, opts []Option) error {
	s := NewService(comp, opts)

	if _, ok := h.services[s.Name]; ok {
		return errors.New("handler 服务已存在: " + s.Name)
	}

	if err := s.ExtractHandler(); err != nil {
		return err
	}

	h.services[s.Name] = s
	for name, handler := range s.Handlers {
		h.handlerPool.Register(s.Name, name, handler)
	}
	return nil
}

// SetHandlerHooks 设置 handler 管道钩子。
func (h *HandlerService) SetHandlerHooks(handlerHooks *pipeline.HandlerHooks) {
	h.handlerHooks = handlerHooks
}

// SetNATSClient 设置 NATS RPC 客户端，用于远程消息分发。
func (h *HandlerService) SetNATSClient(client *cluster.NatsRPCClient) {
	h.natsClient = client
}

// Handle 处理单个连接的消息循环。阻塞直到连接断开。
func (h *HandlerService) Handle(conn acceptor.PlayerConn) {
	a := h.agentFactory.CreateAgent(conn)
	go a.Handle()

	logger.Debugw("新会话建立",
		"会话ID", a.GetSession().ID(),
		"用户", a.GetSession().UID(),
		"远程地址", a.RemoteAddr().String(),
	)

	defer func() {
		a.GetSession().Close()
		logger.Debugw("会话读取协程退出",
			"会话ID", a.GetSession().ID(),
		)
	}()

	for {
		msg, err := conn.GetNextMessage()

		if err != nil {
			if errors.Is(err, net.ErrClosed) || errors.Is(err, session.ErrConnectionClosed) {
				logger.Debugw("连接已关闭，不再读取消息", "错误", err)
			} else {
				if a.GetStatus() != constants.StatusStart {
					logger.Errorw("读取下一条消息失败", "错误", err)
				} else {
					logger.Debugw("初始连接时读取消息失败", "错误", err)
				}
			}
			return
		}

		packets, err := h.decoder.Decode(msg)
		if err != nil {
			logger.Errorw("解码消息失败", "错误", err)
			return
		}

		if len(packets) < 1 {
			logger.Warnw("未读取到任何数据包")
			continue
		}

		for i := range packets {
			if err := h.processPacket(a, packets[i]); err != nil {
				logger.Errorw("处理数据包失败", "错误", err)
				return
			}
		}
	}
}

func (h *HandlerService) processPacket(a agent.Agent, p *packet.Packet) error {
	switch p.Type {
	case packet.Handshake:
		logger.Debug("收到握手数据包")

		handshakeData := &session.HandshakeData{}
		if err := json.Unmarshal(p.Data, handshakeData); err != nil {
			defer a.Close()
			logger.Errorw("握手数据解析失败", "错误", err)
			if serr := a.SendHandshakeErrorResponse(); serr != nil {
				logger.Errorw("发送握手错误响应失败", "错误", serr)
				return serr
			}
			return errors.New("无效的握手数据，会话ID=" + formatSid(a))
		}

		if err := a.GetSession().ValidateHandshake(handshakeData); err != nil {
			defer a.Close()
			logger.Errorw("握手校验失败", "错误", err)
			if serr := a.SendHandshakeErrorResponse(); serr != nil {
				logger.Errorw("发送握手错误响应失败", "错误", serr)
				return serr
			}
			return errors.New("握手校验失败，会话ID=" + formatSid(a))
		}

		if err := a.SendHandshakeResponse(); err != nil {
			logger.Errorw("发送握手响应失败", "错误", err)
			return err
		}
		logger.Debugw("握手成功",
			"会话ID", a.GetSession().ID(),
			"远程地址", a.RemoteAddr().String(),
		)

		a.GetSession().SetHandshakeData(handshakeData)
		a.SetStatus(constants.StatusHandshake)

	case packet.HandshakeAck:
		a.SetStatus(constants.StatusWorking)
		logger.Debugw("收到握手确认",
			"会话ID", a.GetSession().ID(),
			"远程地址", a.RemoteAddr().String(),
		)

	case packet.Data:
		if a.GetStatus() < constants.StatusWorking {
			return errors.New("在未完成握手的连接上收到数据，将关闭会话，远程地址=" +
				a.RemoteAddr().String())
		}

		msg, err := message.Decode(p.Data)
		if err != nil {
			return err
		}
		h.processMessage(a, msg)

	case packet.Heartbeat:
		// 心跳正常
	}

	a.SetLastAt()
	return nil
}

func (h *HandlerService) processMessage(a agent.Agent, msg *message.Message) {
	ctx := context.WithValue(context.Background(), constants.StartTimeKey, time.Now().UnixNano())
	ctx = context.WithValue(ctx, constants.RouteKey, msg.Route)
	ctx = context.WithValue(ctx, constants.SessionCtxKey, a.GetSession())

	r, err := route.Decode(msg.Route)
	if err != nil {
		logger.Errorw("路由解析失败", "错误", err)
		a.AnswerWithError(ctx, msg.ID, err)
		return
	}

	if r.SvType == "" {
		r.SvType = h.serverType
	}

	um := unhandledMessage{
		ctx:   ctx,
		agt:   a,
		route: r,
		msg:   msg,
	}

	if r.SvType == h.serverType {
		h.chLocalProcess <- um
	} else if h.natsClient != nil {
		// 远程分发：通过 NATS RPC 转发到目标服务
		go h.remoteProcess(um.ctx, um.agt, r, um.msg)
	} else {
		logger.Warnw("收到发往其他服务器类型的请求，但 NATS 客户端未配置",
			"目标类型", r.SvType,
			"本地类型", h.serverType,
		)
	}
}

// Dispatch 启动消息分发循环，阻塞调用。
func (h *HandlerService) Dispatch() {
	for {
		select {
		case lm := <-h.chLocalProcess:
			h.localProcess(lm.ctx, lm.agt, lm.route, lm.msg)
		}
	}
}

func (h *HandlerService) localProcess(ctx context.Context, a agent.Agent, rt *route.Route, msg *message.Message) {
	var mid uint
	switch msg.Type {
	case message.Request:
		mid = msg.ID
	case message.Notify:
		mid = 0
	}

	ret, err := h.handlerPool.ProcessHandlerMessage(ctx, rt, h.serializer, h.handlerHooks, a.GetSession(), msg.Data, msg.Type)

	if msg.Type != message.Notify {
		if err != nil {
			logger.Errorw("处理 handler 消息失败", "错误", err)
			a.AnswerWithError(ctx, mid, err)
		} else {
			if e := a.GetSession().ResponseMID(ctx, mid, ret); e != nil {
				logger.Errorw("发送响应失败", "错误", e)
			}
		}
	} else {
		if err != nil {
			logger.Errorw("处理 Notify 消息失败", "错误", err)
		}
	}
}

func (h *HandlerService) remoteProcess(ctx context.Context, a agent.Agent, rt *route.Route, msg *message.Message) {
	rpcType := cluster.RPCTypeUser

	// 获取目标服务的 ServerInfo
	target := &cluster.Server{
		Type: rt.SvType,
	}

	resp, err := h.natsClient.Call(ctx, rpcType, rt, a.GetSession(), msg, target)
	if err != nil {
		logger.Errorw("远程 RPC 调用失败",
			"路由", rt.String(),
			"目标类型", rt.SvType,
			"错误", err,
		)
		if msg.Type == message.Request {
			a.AnswerWithError(ctx, msg.ID, err)
		}
		return
	}

	if resp.Error != nil && resp.Error.Msg != "" {
		if msg.Type == message.Request {
			errMsg := resp.Error.Msg
			if resp.Error.Code != "" {
				errMsg = resp.Error.Code + " - " + resp.Error.Msg
			}
			a.AnswerWithError(ctx, msg.ID, errors.New(errMsg))
		}
		return
	}

	if msg.Type == message.Request {
		if err := a.GetSession().ResponseMID(ctx, msg.ID, resp.Data); err != nil {
			logger.Errorw("发送远程响应失败", "错误", err)
		}
	}
}

// DumpServices 输出所有已注册的服务信息。
func (h *HandlerService) DumpServices() {
	handlers := h.handlerPool.GetHandlers()
	for name := range handlers {
		logger.Infow("已注册 handler",
			"名称", name,
			"原始参数", handlers[name].IsRawArg,
		)
	}
}

func formatSid(a agent.Agent) string {
	return strings.TrimLeft(strings.TrimLeft(a.String(), "远程="), "最后活跃=")
}
