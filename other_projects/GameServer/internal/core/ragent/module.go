package ragent

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	loader "project/conf/schema/gen/loader"
	"project/internal/core/app"
	"project/internal/core/errcode"
	"project/internal/core/nodeid"
	corerpc "project/internal/core/rpc"
	genrpc "project/protocol/gen"
)

const defaultSockPath = "/run/routeragent/ra.sock"

var def *Module

// 连接本机 RouterAgent
type Module struct {
	app.BaseModule
	poster   app.Poster
	ready    *app.Ready
	sockPath string
	nodeID   uint32

	mu     sync.RWMutex
	conn   *net.UnixConn
	sendCh chan wireFrame
	stopCh chan struct{}
	once   sync.Once
}

type wireFrame struct {
	typ    FrameType
	header []byte
	body   []byte
}

// 创建 RouterAgent 模块
func NewModule() *Module {
	return &Module{
		ready:    app.NewReady(),
		sockPath: defaultSockPath,
		sendCh:   make(chan wireFrame, 256),
		stopCh:   make(chan struct{}),
	}
}

func (m *Module) Init(a *app.App) error {
	m.poster = a
	return nil
}

// AfterInit 启动连接循环并暴露包级入口
func (m *Module) AfterInit() error {
	m.nodeID = loadNodeID()
	def = m
	core := corerpc.New(m, corerpc.WithPoster(m.poster))
	corerpc.Init(core)
	genrpc.Init(core)
	go m.connectLoop()
	return nil
}

// 等待首次连接完成
func (m *Module) WaitReady(ctx context.Context) error {
	return m.ready.WaitReady(ctx)
}

// BeforeStop 停止后台协程并关闭连接
func (m *Module) BeforeStop() {
	m.once.Do(func() { close(m.stopCh) })
	m.closeConn()
}

func (m *Module) Fini() {}

// 发送消息到 RouterAgent
func Send(dst uint32, msg proto.Message) error {
	if def == nil {
		return errors.New("ragent not initialized")
	}
	return def.Send(dst, msg)
}

// 发送消息到 RouterAgent
func (m *Module) Send(dst uint32, msg proto.Message) error {
	if msg == nil {
		return errors.New("ragent: nil message")
	}
	body, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	select {
	case <-m.stopCh:
		return errors.New("ragent stopped")
	default:
	}
	m.mu.RLock()
	conn := m.conn
	m.mu.RUnlock()
	if conn == nil {
		return errors.New("ragent not connected")
	}
	select {
	case <-m.stopCh:
		return errors.New("ragent stopped")
	case m.sendCh <- wireFrame{typ: FrameRpcNotify, header: marshalDirectHeader(dst), body: body}:
		return nil
	}
}

// 实现 corerpc.Transport 接口
func (m *Module) SendFrame(target corerpc.Target, header corerpc.Header, body []byte) error {
	select {
	case <-m.stopCh:
		return errors.New("ragent stopped")
	default:
	}
	m.mu.RLock()
	conn := m.conn
	m.mu.RUnlock()
	if conn == nil {
		return errors.New("ragent not connected")
	}
	frameType := FrameRpcRequest
	if header.SeqID == 0 {
		frameType = FrameRpcNotify
	}
	wireHeader := rpcHeaderFromTarget(target, header, m.nodeID)
	select {
	case <-m.stopCh:
		return errors.New("ragent stopped")
	case m.sendCh <- wireFrame{typ: frameType, header: encodeRPCWireHeader(wireHeader), body: body}:
		return nil
	}
}

func (m *Module) connectLoop() {
	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: m.sockPath, Net: "unix"})
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		m.mu.Lock()
		m.conn = conn
		m.mu.Unlock()

		if err := m.writeFrame(conn, Frame{Type: FrameHandshake, Body: encodeHandshakeBody(m.nodeID)}); err != nil {
			m.closeConn()
			continue
		}

		writeDone := make(chan struct{})
		go m.writeLoop(conn, writeDone)

		if err := m.readLoop(conn); err != nil {
			close(writeDone)
			m.closeConn()
			continue
		}

		close(writeDone)
		m.closeConn()
	}
}

func (m *Module) readLoop(conn *net.UnixConn) error {
	for {
		select {
		case <-m.stopCh:
			return nil
		default:
		}
		lengthBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, lengthBuf); err != nil {
			return err
		}
		length := int(binary.BigEndian.Uint32(lengthBuf))
		if length < 3 {
			return errors.New("ragent frame length too short")
		}
		buf := make([]byte, length)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return err
		}
		data := make([]byte, 4+length)
		copy(data[:4], lengthBuf)
		copy(data[4:], buf)
		frame, err := decodeRAFrame(data)
		if err != nil {
			return err
		}
		if err := m.handleInboundFrame(frame, conn); err != nil {
			return err
		}
	}
}

func (m *Module) handleInboundFrame(frame Frame, conn *net.UnixConn) error {
	switch frame.Type {
	case FrameHandshakeAck:
		m.ready.Done()
	case FrameHeartbeat:
		return m.writeFrame(conn, Frame{Type: FrameHeartbeat})
	case FrameRpcResponse:
		head, err := decodeRPCWireHeader(frame.Header)
		if err != nil {
			return nil
		}
		if core := corerpc.Default(); core != nil {
			core.OnResponse(head.SeqID, frame.Body, errcode.ErrCode(head.ErrCode))
		}
	case FrameBroadcastSent:
		// 广播结果当前仅供后续扩展使用
	default:
	}
	return nil
}

func (m *Module) writeLoop(conn *net.UnixConn, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case <-m.stopCh:
			return
		case frame := <-m.sendCh:
			if err := m.writeFrame(conn, Frame{Type: frame.typ, Header: frame.header, Body: frame.body}); err != nil {
				return
			}
		}
	}
}

func (m *Module) writeFrame(conn *net.UnixConn, frame Frame) error {
	data, err := encodeRAFrame(frame.Type, frame.Header, frame.Body)
	if err != nil {
		return err
	}
	_, err = conn.Write(data)
	return err
}

func (m *Module) flushPending() {}

func (m *Module) closeConn() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn != nil {
		_ = m.conn.Close()
		m.conn = nil
	}
}

func marshalDirectHeader(dst uint32) []byte {
	return encodeRPCWireHeader(rpcWireHeader{
		RoutingMode: uint8(corerpc.RoutingDirect),
		RoutingKey:  fmt.Sprintf("%d", dst),
	})
}

func encodeHandshakeBody(nodeID uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, nodeID)
	return buf
}

func loadNodeID() uint32 {
	defer func() { _ = recover() }()
	cfg := loader.CommonConfig()
	if cfg == nil || cfg.Node == nil {
		return nodeid.Encode(0, 0, 0).Uint32()
	}
	return nodeid.Encode(cfg.Node.WorldId, cfg.Node.ServerType, cfg.Node.ServerIndex).Uint32()
}
