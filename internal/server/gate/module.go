package gate

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"project/internal/core/acceptor"
	"project/internal/core/app"
	"project/internal/core/codec"
	"project/internal/core/conn"
	"project/internal/core/dispatcher"
	"project/internal/core/errcode"
	"project/internal/core/ragent"
	corerpc "project/internal/core/rpc"
	"project/internal/core/session"
	"project/internal/server/routeragent"
	"project/pkg/logger"
	"project/protocol/common"
	genrpc "project/protocol/gen"
	remotepb "project/protocol/remote"
)

const moduleName = "gate"

type Module struct {
	app.BaseModule
	ready      *app.Ready
	sessions   *session.SessionManager
	dispatcher *dispatcher.GateDispatcher
	rpcCore    *corerpc.Core
	client     *ragent.Client
	cfg        *GateConfigEntry
	stopCh     chan struct{}
	stopOnce   sync.Once
	acceptors  []acceptor.Acceptor
}

func NewModule() *Module {
	return &Module{ready: app.NewReady(), stopCh: make(chan struct{})}
}

func (m *Module) Name() string { return moduleName }

func (m *Module) Init() error {
	entry := gateConfigEntry
	if entry == nil {
		return fmt.Errorf("gate config entry is nil")
	}
	m.cfg = entry
	cfg := entry.Get()
	if cfg == nil {
		return fmt.Errorf("gate config is nil")
	}
	poster, ok := m.App().(app.Poster)
	if !ok {
		return fmt.Errorf("gate app does not implement poster")
	}

	m.sessions = session.NewSessionManager()
	m.dispatcher = dispatcher.NewGateDispatcher(uint32(common.ServerType_ST_GATESVR), m.sessions)
	m.dispatcher.Use(dispatcher.RecoverMiddleware())
	m.dispatcher.Use(dispatcher.AuthMiddleware(genrpc.AuthWhitelist))
	for cmdID, entry := range genrpc.RouteTable {
		m.dispatcher.RegisterRoute(cmdID, dispatcher.RouteEntry{CmdID: cmdID, ServerType: entry.ServerType, Route: entry.Route, RspCmdID: entry.RspCmdID})
	}

	m.client = ragent.NewClient(m.App().NodeIDUint32(), cfg.RouteragentSockPath, poster, m.handleRagentFrame)
	m.rpcCore = corerpc.New(m.client, corerpc.WithPoster(poster))
	m.client.SetCore(m.rpcCore)
	genrpc.Init(m.rpcCore)
	corerpc.Init(m.rpcCore)
	m.dispatcher.SetForward(m.forwardToBackend)
	m.dispatcher.SetHandshakeHandler(m.handleHandshake)
	return nil
}

func (m *Module) AfterInit() error {
	if err := m.client.Connect(); err != nil {
		m.ready.Fail(err)
		return err
	}
	cfg := m.cfg.Get()
	if cfg.ListenTcp != "" {
		acc := acceptor.NewTCPAcceptor(cfg.ListenTcp)
		if err := m.startAcceptor(acc); err != nil {
			m.ready.Fail(err)
			return err
		}
	}
	if cfg.ListenWs != "" {
		acc := acceptor.NewWSAcceptor(cfg.ListenWs, "/")
		if err := m.startAcceptor(acc); err != nil {
			m.ready.Fail(err)
			return err
		}
	}
	m.ready.Done()
	return nil
}

func (m *Module) WaitReady(ctx context.Context) error { return m.ready.WaitReady(ctx) }

func (m *Module) BeforeShutdown() {
	m.stopOnce.Do(func() { close(m.stopCh) })
	for _, acc := range m.acceptors {
		_ = acc.Close()
	}
	if m.client != nil {
		_ = m.client.Close()
	}
}

func (m *Module) Shutdown() {}

func (m *Module) startAcceptor(acc acceptor.Acceptor) error {
	if err := acc.Listen(); err != nil {
		return err
	}
	m.acceptors = append(m.acceptors, acc)
	go m.acceptLoop(acc)
	return nil
}

func (m *Module) acceptLoop(acc acceptor.Acceptor) {
	for {
		select {
		case <-m.stopCh:
			return
		case c, ok := <-acc.Accept():
			if !ok {
				return
			}
			m.handleConn(c)
		}
	}
}

func (m *Module) handleConn(c conn.Connection) {
	m.sessions.OnConnect(c)
	go m.recvLoop(c)
}

func (m *Module) recvLoop(c conn.Connection) {
	defer func() {
		m.sessions.OnDisconnect(c)
		_ = c.Close()
	}()
	for {
		select {
		case <-m.stopCh:
			return
		case <-c.Done():
			return
		case pkt, ok := <-c.Recv():
			if !ok {
				return
			}
			packet := pkt
			m.App().Post(func() {
				_ = m.dispatcher.HandlePacket(c, packet)
			})
		}
	}
}

func (m *Module) handleHandshake(c conn.Connection, _ []byte) bool {
	pkt, err := codec.EncodePacket(codec.Packet{Type: codec.PacketHandshakeAck, Body: []byte{1}})
	if err != nil {
		return false
	}
	c.Send(pkt)
	return true
}

func (m *Module) forwardToBackend(sess *session.Session, msg *codec.Message, entry dispatcher.RouteEntry) error {
	if sess == nil || msg == nil {
		return errcode.New(errcode.ERR_UNMARSHAL, "nil session or message")
	}
	target := corerpc.Target{ServerType: entry.ServerType}.ByHash(sess.ConnID)
	if nodeID := sess.BoundNodes[entry.ServerType]; nodeID != 0 {
		target = target.At(nodeID)
	}
	ctx := corerpc.Background().WithFromNode(m.App().NodeIDUint32())
	sessID := sess.ID
	sessConnID := sess.ConnID
	switch msg.Type {
	case codec.MessageRequest:
		if entry.Route == "LobbyHandler/Ping" {
			logger.Info("ping gate forward to lobby",
				logger.Uint32("cmd_id", msg.CmdID),
				logger.Uint32("rsp_cmd_id", entry.RspCmdID),
				logger.Uint64("client_seq", uint64(msg.SeqID)),
				logger.String("session_id", sessID),
				logger.String("route", entry.Route),
				logger.Uint32("server_type", entry.ServerType),
				logger.Uint32("from_node", m.App().NodeIDUint32()))
		}
		m.rpcCore.Call(target, entry.Route, msg.Body, ctx, func(payload []byte, code errcode.ErrCode) {
			if entry.Route == "LobbyHandler/Ping" {
				logger.Info("tong gate receive from lobby",
					logger.Uint32("rsp_cmd_id", entry.RspCmdID),
					logger.Uint64("client_seq", uint64(msg.SeqID)),
					logger.String("session_id", sessID),
					logger.String("route", entry.Route),
					logger.Uint32("err_code", uint32(code)),
					logger.Int("payload_len", len(payload)))
			}
			current := m.sessions.GetByConnID(sessConnID)
			if current == nil || current.ID != sessID || current.Conn == nil {
				return
			}
			rsp, encErr := codec.EncodeMessage(codec.Message{Type: codec.MessageResponse, SeqID: msg.SeqID, CmdID: entry.RspCmdID, ErrCode: code, Body: payload})
			if encErr != nil {
				return
			}
			pkt, encErr := codec.EncodePacket(codec.Packet{Type: codec.PacketData, Body: rsp})
			if encErr != nil {
				return
			}
			if entry.Route == "LobbyHandler/Ping" {
				logger.Info("tong gate send to client",
					logger.Uint32("rsp_cmd_id", entry.RspCmdID),
					logger.Uint64("client_seq", uint64(msg.SeqID)),
					logger.String("session_id", sessID),
					logger.Uint32("err_code", uint32(code)),
					logger.Int("packet_len", len(pkt)))
			}
			current.Conn.Send(pkt)
		})
	case codec.MessageNotify:
		m.rpcCore.Send(target, entry.Route, msg.Body, ctx)
	default:
		return errcode.New(errcode.ERR_UNMARSHAL, "unsupported forwarded message type")
	}
	return nil
}

func (m *Module) handleRagentFrame(frame routeragent.Frame) {
	switch frame.Type {
	case routeragent.FrameRpcRequest, routeragent.FrameRpcNotify:
		m.handleRemote(frame)
	}
}

func (m *Module) handleRemote(frame routeragent.Frame) {
	head, err := routeragent.DecodeRPCWireHeader(frame.Header)
	if err != nil {
		return
	}
	ctx := corerpc.Background().WithFromNode(head.FromNodeID).WithDeadline(time.Duration(head.DeadlineMs) * time.Millisecond)
	var code errcode.ErrCode = errcode.OK
	switch head.Route {
	case "GateRemote/SendToClient":
		var ntf remotepb.RPC_SendToClient_Ntf
		if err := proto.Unmarshal(frame.Body, &ntf); err != nil {
			code = errcode.ERR_UNMARSHAL
		} else {
			m.SendToClient(ctx, &ntf)
		}
	case "GateRemote/BindSession":
		var ntf remotepb.RPC_BindSession_Ntf
		if err := proto.Unmarshal(frame.Body, &ntf); err != nil {
			code = errcode.ERR_UNMARSHAL
		} else {
			m.BindSession(ctx, &ntf)
		}
	case "GateRemote/SetBound":
		var ntf remotepb.RPC_SetBound_Ntf
		if err := proto.Unmarshal(frame.Body, &ntf); err != nil {
			code = errcode.ERR_UNMARSHAL
		} else {
			m.SetBound(ctx, &ntf)
		}
	default:
		code = errcode.ERR_NO_ROUTE
	}
	if frame.Type == routeragent.FrameRpcRequest && head.SeqID != 0 {
		head.ErrCode = uint32(code)
		_ = m.client.Send(routeragent.Frame{Type: routeragent.FrameRpcResponse, Header: routeragent.EncodeRPCWireHeader(head)})
	}
}

func (m *Module) SendToClient(_ corerpc.Ctx, ntf *remotepb.RPC_SendToClient_Ntf) {
	if ntf == nil || ntf.GetCmdId() == 0 {
		return
	}
	var sess *session.Session
	if ntf.GetUid() != 0 {
		sess = m.sessions.GetByUID(ntf.GetUid())
	}
	if sess == nil && ntf.GetSessionId() != "" {
		sess = m.sessions.GetByConnID(ntf.GetSessionId())
	}
	if sess == nil || sess.Conn == nil {
		return
	}
	body, err := codec.EncodeMessage(codec.Message{Type: codec.MessageNotify, CmdID: ntf.GetCmdId(), Body: ntf.GetPayload()})
	if err != nil {
		return
	}
	pkt, err := codec.EncodePacket(codec.Packet{Type: codec.PacketData, Body: body})
	if err != nil {
		return
	}
	sess.Conn.Send(pkt)
}

func (m *Module) BindSession(_ corerpc.Ctx, ntf *remotepb.RPC_BindSession_Ntf) {
	if ntf == nil || ntf.GetSessionId() == "" {
		return
	}
	m.sessions.BindSession(ntf.GetSessionId(), ntf.GetUid(), ntf.GetBoundNodes())
}

func (m *Module) SetBound(_ corerpc.Ctx, ntf *remotepb.RPC_SetBound_Ntf) {
	if ntf == nil || ntf.GetUid() == 0 || ntf.GetServerType() == 0 || ntf.GetNodeId() == 0 {
		return
	}
	m.sessions.SetBound(ntf.GetUid(), ntf.GetServerType(), ntf.GetNodeId())
}
