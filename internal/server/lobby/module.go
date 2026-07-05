package lobby

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"project/internal/core/app"
	"project/internal/core/errcode"
	"project/internal/core/ragent"
	corerpc 	"project/internal/core/rpc"
	"project/internal/server/routeragent"
	handlerpb "project/protocol/handler"
)

const moduleName = "lobby"

type routeragentClient interface {
	Connect() error
	Close() error
	Send(routeragent.Frame) error
}

type Module struct {
	app.BaseModule
	ready    *app.Ready
	cfg      *LobbyConfigEntry
	client   routeragentClient
	handler  *Handler
	stopOnce sync.Once
}

func NewModule() *Module {
	return &Module{ready: app.NewReady()}
}

func (m *Module) Name() string { return moduleName }

func (m *Module) Init() error {
	entry := lobbyConfigEntry
	if entry == nil {
		return fmt.Errorf("lobby config entry is nil")
	}
	m.cfg = entry
	cfg := entry.Get()
	if cfg == nil {
		return fmt.Errorf("lobby config is nil")
	}
	poster, ok := m.App().(app.Poster)
	if !ok {
		return fmt.Errorf("lobby app does not implement poster")
	}
	m.handler = NewHandler()
	m.client = ragent.NewClient(m.App().NodeIDUint32(), cfg.RouteragentSockPath, poster, m.handleRagentFrame)
	return nil
}

func (m *Module) AfterInit() error {
	if err := m.client.Connect(); err != nil {
		m.ready.Fail(err)
		return err
	}
	m.ready.Done()
	return nil
}

func (m *Module) WaitReady(ctx context.Context) error { return m.ready.WaitReady(ctx) }

func (m *Module) BeforeShutdown() {
	m.stopOnce.Do(func() {
		if m.client != nil {
			_ = m.client.Close()
		}
	})
}

func (m *Module) Shutdown() {}

func (m *Module) handleRagentFrame(frame routeragent.Frame) {
	switch frame.Type {
	case routeragent.FrameRpcRequest, routeragent.FrameRpcNotify:
		m.handleInbound(frame)
	}
}

func (m *Module) handleInbound(frame routeragent.Frame) {
	head, err := routeragent.DecodeRPCWireHeader(frame.Header)
	if err != nil {
		return
	}
	var code errcode.ErrCode = errcode.OK
	if err := m.dispatchRoute(head, frame.Body, func(payload []byte, err error) {
		if frame.Type != routeragent.FrameRpcRequest || head.SeqID == 0 {
			return
		}
		rspHead := responseHead(head, m.nodeID())
		rspHead.ErrCode = uint32(errcode.CodeOf(err))
		_ = m.client.Send(routeragent.Frame{Type: routeragent.FrameRpcResponse, Header: routeragent.EncodeRPCWireHeader(rspHead), Body: payload})
	}); err != nil {
		code = errcode.CodeOf(err)
	}
	if frame.Type == routeragent.FrameRpcRequest && head.SeqID != 0 && code != errcode.OK {
		rspHead := responseHead(head, m.nodeID())
		rspHead.ErrCode = uint32(code)
		_ = m.client.Send(routeragent.Frame{Type: routeragent.FrameRpcResponse, Header: routeragent.EncodeRPCWireHeader(rspHead)})
	}
}

func (m *Module) dispatchRoute(head routeragent.RPCWireHeader, body []byte, reply func([]byte, error)) error {
	ctx := inboundCtx(head)
	switch head.Route {
	case "LobbyHandler/ClaimReward":
		var req handlerpb.CS_ClaimReward_Req
		if err := proto.Unmarshal(body, &req); err != nil {
			return errcode.New(errcode.ERR_UNMARSHAL, err.Error())
		}
		m.handler.ClaimReward(ctx, &req, replyHandler[*handlerpb.SC_ClaimReward_Rsp](reply))
		return nil
	case "LobbyHandler/SyncPos":
		var ntf handlerpb.CS_SyncPos_Ntf
		if err := proto.Unmarshal(body, &ntf); err != nil {
			return errcode.New(errcode.ERR_UNMARSHAL, err.Error())
		}
		m.handler.SyncPos(ctx, &ntf)
		return nil
	case "LobbyHandler/Ping":
		var req handlerpb.CS_Ping_Req
		if err := proto.Unmarshal(body, &req); err != nil {
			return errcode.New(errcode.ERR_UNMARSHAL, err.Error())
		}
		m.handler.Ping(ctx, &req, replyHandler[*handlerpb.SC_Tong_Rsp](reply))
		return nil
	default:
		return errcode.New(errcode.ERR_NO_ROUTE, "route not found: "+head.Route)
	}
}

func (m *Module) nodeID() uint32 {
	if m.App() == nil {
		return 0
	}
	return m.App().NodeIDUint32()
}

func responseHead(req routeragent.RPCWireHeader, localNodeID uint32) routeragent.RPCWireHeader {
	rsp := req
	if rsp.DestNodeID != 0 {
		rsp.SrcNodeID = rsp.DestNodeID
	} else if localNodeID != 0 {
		rsp.SrcNodeID = localNodeID
	}
	rsp.DestNodeID = req.SrcNodeID
	return rsp
}

func replyHandler[T proto.Message](reply func([]byte, error)) corerpc.Reply[T] {
	return func(rsp T, err error) {
		if reply == nil {
			return
		}
		if err != nil {
			reply(nil, err)
			return
		}
		if proto.Message(rsp) == nil {
			reply(nil, errcode.New(errcode.ERR_INTERNAL, "nil response"))
			return
		}
		payload, merr := proto.Marshal(rsp)
		if merr != nil {
			reply(nil, errcode.New(errcode.ERR_INTERNAL, merr.Error()))
			return
		}
		reply(payload, nil)
	}
}

func inboundCtx(head routeragent.RPCWireHeader) corerpc.Ctx {
	ctx := corerpc.Background().WithFromNode(head.SrcNodeID)
	if head.DeadlineMs > 0 {
		ctx = ctx.WithDeadline(time.Duration(head.DeadlineMs) * time.Millisecond)
	}
	return ctx
}

func NewModuleForTest() *Module {
	return &Module{ready: app.NewReady(), handler: NewHandler()}
}

func (m *Module) HandleRagentFrame(frame routeragent.Frame) {
	m.handleRagentFrame(frame)
}

func (m *Module) SetClient(client routeragentClient) {
	m.client = client
}
