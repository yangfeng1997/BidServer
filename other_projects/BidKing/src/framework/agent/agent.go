package agent

import (
	"context"
	"errors"
	"net"
	"project/src/common/logger"
	"project/src/framework/handler"
	"project/src/framework/network/acceptor"
	"project/src/framework/network/handshake"
	"project/src/framework/network/message"
	"project/src/framework/network/packet"
	"project/src/framework/session"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

const (
	writeTimeout = 10 * time.Second
	// defaultHeartbeatSec 当 Factory 未传有效心跳间隔时的兜底值（秒）
	defaultHeartbeatSec = 30
	sendChanSize        = 64
)

// status 连接状态
type status int32

const (
	statusInit    status = 0 // 连接刚建立，等待客户端发 Handshake
	statusWaitAck status = 1 // 已发握手响应，等待客户端发 HandshakeAck
	statusWorking status = 2 // 握手完成，正常收发业务数据
	statusClosed  status = 3 // 连接已关闭
)

// ErrSendBufferFull Push 时发送缓冲区已满返回此错误
var ErrSendBufferFull = errors.New("agent send buffer full")

// Agent 客户端连接的业务抽象
type Agent interface {
	Session() *session.Session
	Push(msgID uint32, data []byte) error
	Response(mid uint32, msgID uint32, data []byte) error
	ResponseErr(mid uint32, msgID uint32, code int32) error
	Close() error
	RemoteAddr() net.Addr
	IsAlive() bool
	OnClose(fn func(*session.Session))
}

// ForwardContext 转发函数的参数，包含转发所需的全部信息
type ForwardContext struct {
	Agent      Agent
	MsgID      uint32 // 客户端消息 ID
	MID        uint16 // 客户端请求 MID（0 表示 OneWay）
	MsgType    uint8  // message.Type：1=Request, 3=OneWay
	Data       []byte // 业务 payload
	RespMsgID  uint32 // 响应消息 ID（Request 时有效）
	ServerType string // 目标服务类型
}

type connAgent struct {
	conn     acceptor.ClientConn
	session  *session.Session
	sessions *session.Manager
	chSend   chan []byte
	chDie    chan struct{}
	once     sync.Once
	wg       *sync.WaitGroup
	registry *handler.Registry
	state    atomic.Int32
	lastAt   atomic.Int64

	// 每连接生命周期 ctx：连接关闭（断连/写错误/心跳超时/停机强关）时取消，
	// 令在途下游工作（gate 转发 CallRaw、尊重 ctx 的本地 handler）迅速中止。
	ctx    context.Context
	cancel context.CancelFunc

	// 生命周期管理（从 Session 移入）
	onCloseCallbacks []func(*session.Session)

	// 握手配置
	validators     []handshake.Validator
	heartbeatSec   int
	serializerName string

	// 路由表（由 Application 注入）
	msgRouteTable  map[uint32]string
	forwardTable   map[uint32]string
	respMsgIDTable map[uint32]uint32
	forwardFn      func(ctx context.Context, fctx *ForwardContext)
}

var _ Agent = (*connAgent)(nil)

func (a *connAgent) getStatus() status         { return status(a.state.Load()) }
func (a *connAgent) setStatus(s status)        { a.state.Store(int32(s)) }
func (a *connAgent) Session() *session.Session { return a.session }
func (a *connAgent) RemoteAddr() net.Addr      { return a.conn.RemoteAddr() }
func (a *connAgent) IsAlive() bool             { return a.getStatus() == statusWorking }

// SessionProvider 实现：session 数据供 registry.Dispatch 注入 ctx
func (a *connAgent) SessionID() int64   { return a.session.ID() }
func (a *connAgent) UID() int64         { return a.session.UID() }
func (a *connAgent) IP() string         { return a.session.IP() }
func (a *connAgent) FrontendID() string { return a.session.FrontendID() }

// OnClose 注册连接关闭回调
func (a *connAgent) OnClose(fn func(*session.Session)) {
	a.onCloseCallbacks = append(a.onCloseCallbacks, fn)
}

// Push 服务端主动推送（OneWay，无 MID）
func (a *connAgent) Push(msgID uint32, data []byte) error {
	return a.sendMessage(message.NewOneWay(msgID, data))
}

// Response 回复客户端请求（Response，携带原始 MID 和 msgID，Code=0）
func (a *connAgent) Response(mid uint32, msgID uint32, data []byte) error {
	return a.sendMessage(message.NewResponse(uint16(mid), msgID, data))
}

// ResponseErr 回带框架错误码的 Response（无 data），code 为负数
func (a *connAgent) ResponseErr(mid uint32, msgID uint32, code int32) error {
	return a.sendMessage(message.NewErrorResponse(uint16(mid), msgID, code))
}

func (a *connAgent) sendMessage(msg *message.Message) error {
	body, err := message.Encode(msg)
	if err != nil {
		return err
	}
	return a.sendFrame(packet.Encode(packet.Data, body))
}

func (a *connAgent) sendFrame(frame []byte) error {
	select {
	case a.chSend <- frame:
		return nil
	case <-a.chDie:
		return net.ErrClosed
	default:
		return ErrSendBufferFull
	}
}

func (a *connAgent) Close() error {
	a.once.Do(func() {
		a.setStatus(statusClosed)
		close(a.chDie)
		a.cancel()
		a.conn.Close()
		a.sessions.Close(a.session)
		for _, cb := range a.onCloseCallbacks {
			a.safeCloseCallback(cb)
		}
	})
	return nil
}

// safeCloseCallback 执行单个 OnClose 回调并隔离 panic：
// 回调由业务注册（不可信），单个崩溃不应中断整条关闭流程。
func (a *connAgent) safeCloseCallback(cb func(*session.Session)) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Error("agent: OnClose callback panic",
				logger.Int64("sessionID", a.session.ID()),
				logger.String("stack", string(debug.Stack())))
		}
	}()
	cb(a.session)
}

// Handle 启动读写两个 goroutine（心跳已并入 write），阻塞直到连接关闭。
// read 阻塞在 ReadPacket 上不可省；write 兼任心跳定时，每连接仅 2 个 goroutine。
func (a *connAgent) Handle() {
	defer a.wg.Done()
	if a.heartbeatSec <= 0 {
		a.heartbeatSec = defaultHeartbeatSec
	}
	a.setStatus(statusInit)
	a.lastAt.Store(time.Now().UnixNano())
	go a.write()
	a.read()
}

func (a *connAgent) read() {
	defer a.Close()
	for {
		pkt, err := a.conn.ReadPacket()
		if err != nil {
			return
		}
		a.lastAt.Store(time.Now().UnixNano())
		if err := a.handlePacket(pkt); err != nil {
			return
		}
	}
}

func (a *connAgent) handlePacket(pkt *packet.Packet) error {
	switch pkt.Type {
	case packet.Handshake:
		if a.getStatus() != statusInit {
			return nil
		}
		return a.handleHandshake(pkt.Body)
	case packet.HandshakeAck:
		if a.getStatus() == statusWaitAck {
			a.setStatus(statusWorking)
		}
		return nil
	case packet.Heartbeat:
		return nil
	case packet.Data:
		if a.getStatus() != statusWorking {
			return errors.New("received data before handshake complete")
		}
		return a.handleData(pkt.Body)
	default:
		return nil
	}
}

func (a *connAgent) handleHandshake(body []byte) error {
	req, err := handshake.DecodeRequest(body)
	if err != nil {
		a.sendHandshakeErr()
		return err
	}
	for _, v := range a.validators {
		if err := v(req); err != nil {
			a.sendHandshakeErr()
			return err
		}
	}
	resp := handshake.OKResponse(a.heartbeatSec, a.serializerName)
	respBody, err := handshake.EncodeResponse(resp)
	if err != nil {
		return err
	}
	a.setStatus(statusWaitAck)
	return a.sendFrame(packet.Encode(packet.Handshake, respBody))
}

func (a *connAgent) sendHandshakeErr() {
	resp := handshake.ErrResponse()
	if body, err := handshake.EncodeResponse(resp); err == nil {
		a.sendFrame(packet.Encode(packet.Handshake, body))
	}
}

func (a *connAgent) handleData(body []byte) error {
	msg, err := message.Decode(body)
	if err != nil {
		// 畸形包：记录并断连，避免后续帧错位
		logger.Warn("agent: message decode failed, closing connection",
			logger.Int64("sessionID", a.session.ID()), logger.Err(err))
		return err
	}

	ctx := a.ctx

	// 查本地路由表：仅当本地 registry 真有该 handler 才本地分发，
	// 否则落入下方转发（携 handler_method 的转发消息在 gate 上无本地 handler → 转发）。
	if route, ok := a.msgRouteTable[msg.MsgID]; ok && a.registry.HasRoute(route) {
		respMsgID := a.respMsgIDTable[msg.MsgID]
		// Dispatch 内 pcall 已把 handler panic 转为 error，这里务必记录，
		// 否则线上 handler 崩溃将无任何痕迹。错误不断连（单条消息失败不影响连接）。
		if err := a.registry.Dispatch(ctx, a, route, uint32(msg.MID), respMsgID, msg.Data); err != nil {
			logger.Error("agent: handler dispatch failed",
				logger.String("route", route),
				logger.Uint32("msgID", msg.MsgID),
				logger.Int64("uid", a.session.UID()),
				logger.Err(err))
		}
		return nil
	}

	// 查转发表（gate 场景）
	if a.forwardFn != nil {
		if serverType, ok := a.forwardTable[msg.MsgID]; ok {
			fctx := &ForwardContext{
				Agent:      a,
				MsgID:      msg.MsgID,
				MID:        msg.MID,
				MsgType:    uint8(msg.Type),
				Data:       msg.Data,
				RespMsgID:  a.respMsgIDTable[msg.MsgID],
				ServerType: serverType,
			}
			a.forwardFn(ctx, fctx)
			return nil
		}
	}

	return nil
}

// write 是连接唯一写 goroutine：消费 chSend 发送业务帧，并兼任心跳定时。
// 心跳合并进此 select（替代独立的 heartbeat goroutine），每连接因此只需 2 个 goroutine。
func (a *connAgent) write() {
	hbFrame := packet.EncodeHeartbeat()
	ticker := time.NewTicker(time.Duration(a.heartbeatSec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case frame := <-a.chSend:
			if !a.writeFrame(frame) {
				return
			}
		case <-ticker.C:
			// 最后活动早于 2 个心跳周期则判定超时断连
			deadline := time.Now().Add(-2 * time.Duration(a.heartbeatSec) * time.Second).UnixNano()
			if a.lastAt.Load() < deadline {
				a.Close()
				return
			}
			// 已在 write goroutine 内，直接写而不经 chSend，省一次 channel 往返、
			// 也避免心跳占用发送缓冲。写串行性不变（仅此 goroutine 碰 conn 写）。
			if !a.writeFrame(hbFrame) {
				return
			}
		case <-a.chDie:
			return
		}
	}
}

// writeFrame 向连接写一帧，失败时关闭连接并返回 false。仅供 write goroutine 调用。
func (a *connAgent) writeFrame(frame []byte) bool {
	a.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if _, err := a.conn.Write(frame); err != nil {
		a.Close()
		return false
	}
	return true
}
