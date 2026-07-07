package lobby

import (
	"context"
	"fmt"
	"sync"
	"time"

	"project/internal/core/app"
	"project/internal/core/errcode"
	"project/internal/core/ragent/sdk"
	ragentwire "project/internal/core/ragent/wire"
	corerpc "project/internal/core/rpc"
	lobbyinternal "project/internal/server/lobby/internal"
	"project/pkg/mongo"
	genhandler "project/protocol/gen/handler"
)

const moduleName = "lobby"

type routeragentClient interface {
	Connect() error
	Close() error
	Send(ragentwire.Frame) error
	SendRPCFrame(ragentwire.FrameType, ragentwire.RPCWireHeader, []byte) error
}

type Module struct {
	app.BaseModule
	ready         *app.Ready
	cfg           *LobbyConfigEntry
	client        routeragentClient
	handler       *Handler
	runtime       *lobbyinternal.Runtime
	mongoClient   *mongo.Client
	rpcDispatcher *corerpc.Dispatcher
	stopOnce      sync.Once
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
	commonCfg := CommonConfig()
	if commonCfg == nil {
		return fmt.Errorf("common config is nil")
	}
	mc, err := mongo.Connect(commonCfg.Mongo.Uri, commonCfg.Mongo.Database, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connect mongo: %w", err)
	}
	m.mongoClient = mc
	m.handler = NewHandler()
	m.runtime = newRuntime(m.App().NodeID(), mc)
	m.rpcDispatcher = corerpc.NewDispatcher()
	genhandler.RegisterLobbyHandler(m.rpcDispatcher, m.handler)
	m.client = sdk.NewClient(m.App().NodeIDUint32(), cfg.RouteragentSockPath, poster, m.handleRagentFrame)
	return nil
}

func (m *Module) AfterInit() error {
	if m.runtime != nil {
		m.runtime.Start()
	}
	if err := m.client.Connect(); err != nil {
		m.ready.Fail(err)
		if m.runtime != nil {
			m.runtime.Stop()
		}
		if m.mongoClient != nil {
			_ = m.mongoClient.Close()
		}
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
		if m.runtime != nil {
			m.runtime.Stop()
		}
		if m.mongoClient != nil {
			_ = m.mongoClient.Close()
		}
	})
}

func (m *Module) Shutdown() {}

func (m *Module) handleRagentFrame(frame ragentwire.Frame) {
	switch frame.Type {
	case ragentwire.FrameRpcRequest, ragentwire.FrameRpcNotify:
		m.handleInbound(frame)
	}
}

func (m *Module) handleInbound(frame ragentwire.Frame) {
	head, err := ragentwire.DecodeRPCWireHeader(frame.Header)
	if err != nil {
		return
	}
	var code errcode.ErrCode = errcode.OK
	if err := m.dispatchRoute(head, frame.Body, func(payload []byte, err error) {
		if frame.Type != ragentwire.FrameRpcRequest || head.SeqID == 0 {
			return
		}
		rspHead := responseHead(head, m.nodeID())
		rspHead.ErrCode = uint32(errcode.CodeOf(err))
		_ = m.client.SendRPCFrame(ragentwire.FrameRpcResponse, rspHead, payload)
	}); err != nil {
		code = errcode.CodeOf(err)
	}
	if frame.Type == ragentwire.FrameRpcRequest && head.SeqID != 0 && code != errcode.OK {
		rspHead := responseHead(head, m.nodeID())
		rspHead.ErrCode = uint32(code)
		_ = m.client.SendRPCFrame(ragentwire.FrameRpcResponse, rspHead, nil)
	}
}

func (m *Module) dispatchRoute(head ragentwire.RPCWireHeader, body []byte, reply func([]byte, error)) error {
	return m.rpcDispatcher.Dispatch(head.Route, inboundCtx(head), body, reply)
}

func (m *Module) nodeID() uint32 {
	if m.App() == nil {
		return 0
	}
	return m.App().NodeIDUint32()
}

func responseHead(req ragentwire.RPCWireHeader, localNodeID uint32) ragentwire.RPCWireHeader {
	rsp := req
	if rsp.DestNodeID != 0 {
		rsp.SrcNodeID = rsp.DestNodeID
	} else if localNodeID != 0 {
		rsp.SrcNodeID = localNodeID
	}
	rsp.DestNodeID = req.SrcNodeID
	return rsp
}

func inboundCtx(head ragentwire.RPCWireHeader) corerpc.Ctx {
	ctx := corerpc.Background().WithFromNode(head.SrcNodeID)
	if head.DeadlineMs > 0 {
		ctx = ctx.WithDeadline(time.Duration(head.DeadlineMs) * time.Millisecond)
	}
	return ctx
}

func NewModuleForTest() *Module {
	handler := NewHandler()
	dispatcher := corerpc.NewDispatcher()
	genhandler.RegisterLobbyHandler(dispatcher, handler)
	return &Module{ready: app.NewReady(), handler: handler, rpcDispatcher: dispatcher}
}

func newRuntime(nodeID string, mc *mongo.Client) *lobbyinternal.Runtime {
	return lobbyinternal.NewRuntime(lobbyinternal.RuntimeConfig{
		NodeID:       nodeID,
		Store:        lobbyinternal.NewMongoStore(mc),
		MailStore:    lobbyinternal.NewMongoMailStore(mc),
		OfflineStore: lobbyinternal.NewMongoOfflineStore(mc),
	})
}

func (m *Module) HandleRagentFrame(frame ragentwire.Frame) {
	m.handleRagentFrame(frame)
}

func (m *Module) SetClient(client routeragentClient) {
	m.client = client
}
