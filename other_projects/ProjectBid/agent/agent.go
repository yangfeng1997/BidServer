// Package agent 定义客户端连接代理，实现握手状态机、心跳检测与消息发送。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"projectbid/server/conn/codec"
	"projectbid/server/conn/message"
	"projectbid/server/conn/packet"
	"projectbid/server/acceptor"
	"projectbid/server/constants"
	"projectbid/server/errors"
	"projectbid/server/serialize"
	"projectbid/server/networkentity"
	"projectbid/server/session"
	"projectbid/server/logger"
	"projectbid/server/util/compression"
)

// Agent 对应一个用户连接，实现握手、心跳和消息收发。
type Agent interface {
	networkentity.NetworkEntity

	GetSession() session.Session
	RemoteAddr() net.Addr
	String() string

	GetStatus() int32
	SetStatus(state int32)
	SetLastAt()

	Handle()
	Close() error
	Kick(ctx context.Context) error

	SendHandshakeResponse() error
	SendHandshakeErrorResponse() error
	AnswerWithError(ctx context.Context, mid uint, err error)
}

// AgentFactory 创建 Agent 实例。
type AgentFactory interface {
	CreateAgent(conn acceptor.PlayerConn) Agent
}

// NewAgentFactory 创建 Agent 工厂。
func NewAgentFactory(
	decoder codec.PacketDecoder,
	encoder codec.PacketEncoder,
	serializer serialize.Serializer,
	heartbeatTimeout time.Duration,
	writeTimeout time.Duration,
	messageEncoder message.Encoder,
	messagesBufferSize int,
	sessionPool session.SessionPool,
) AgentFactory {
	return &agentFactoryImpl{
		decoder:            decoder,
		encoder:            encoder,
		serializer:         serializer,
		heartbeatTimeout:   heartbeatTimeout,
		writeTimeout:       writeTimeout,
		messageEncoder:     messageEncoder,
		messagesBufferSize: messagesBufferSize,
		sessionPool:        sessionPool,
	}
}

type agentFactoryImpl struct {
	decoder            codec.PacketDecoder
	encoder            codec.PacketEncoder
	serializer         serialize.Serializer
	heartbeatTimeout   time.Duration
	writeTimeout       time.Duration
	messageEncoder     message.Encoder
	messagesBufferSize int
	sessionPool        session.SessionPool
}

func (f *agentFactoryImpl) CreateAgent(conn acceptor.PlayerConn) Agent {
	return newAgent(conn, f.decoder, f.encoder, f.serializer, f.heartbeatTimeout, f.writeTimeout, f.messagesBufferSize, f.messageEncoder, f.sessionPool)
}

// ——— agentImpl ———

type agentImpl struct {
	Session     session.Session
	sessionPool session.SessionPool

	chDie           chan struct{}
	chSend          chan pendingWrite
	chStopHeartbeat chan struct{}
	chStopWrite     chan struct{}
	closeMutex      sync.Mutex

	conn               acceptor.PlayerConn
	decoder            codec.PacketDecoder
	encoder            codec.PacketEncoder
	messageEncoder     message.Encoder
	serializer         serialize.Serializer
	heartbeatTimeout   time.Duration
	writeTimeout       time.Duration
	lastAt             int64
	state              int32
	messagesBufferSize int
}

type pendingMessage struct {
	ctx     context.Context
	typ     message.Type
	route   string
	mid     uint
	payload interface{}
	err     bool
}

type pendingWrite struct {
	ctx  context.Context
	data []byte
	err  error
}

var (
	hbd  []byte // 心跳包数据
	hrd  []byte // 握手响应数据
	herd []byte // 握手错误响应数据
	once sync.Once
)

func newAgent(
	conn acceptor.PlayerConn,
	packetDecoder codec.PacketDecoder,
	packetEncoder codec.PacketEncoder,
	serializer serialize.Serializer,
	heartbeatTimeout time.Duration,
	writeTimeout time.Duration,
	messagesBufferSize int,
	messageEncoder message.Encoder,
	sessionPool session.SessionPool,
) Agent {
	serializerName := serializer.GetName()

	once.Do(func() {
		hbdEncode(heartbeatTimeout, packetEncoder, messageEncoder.IsCompressionEnabled(), serializerName)
		herdEncode(heartbeatTimeout, packetEncoder, messageEncoder.IsCompressionEnabled(), serializerName)
	})

	a := &agentImpl{
		chDie:              make(chan struct{}),
		chSend:             make(chan pendingWrite, messagesBufferSize),
		chStopHeartbeat:    make(chan struct{}),
		chStopWrite:        make(chan struct{}),
		messagesBufferSize: messagesBufferSize,
		conn:               conn,
		decoder:            packetDecoder,
		encoder:            packetEncoder,
		messageEncoder:     messageEncoder,
		serializer:         serializer,
		heartbeatTimeout:   heartbeatTimeout,
		writeTimeout:       writeTimeout,
		lastAt:             time.Now().Unix(),
		state:              constants.StatusStart,
		sessionPool:        sessionPool,
	}

	s := sessionPool.NewSession(a)
	a.Session = s
	return a
}

// GetSession 返回绑定的会话。
func (a *agentImpl) GetSession() session.Session { return a.Session }

// RemoteAddr 返回远程地址。
func (a *agentImpl) RemoteAddr() net.Addr { return a.conn.RemoteAddr() }

// String 返回可读描述。
func (a *agentImpl) String() string {
	return fmt.Sprintf("远程=%s, 最后活跃=%d", a.conn.RemoteAddr().String(), atomic.LoadInt64(&a.lastAt))
}

// GetStatus 获取状态。
func (a *agentImpl) GetStatus() int32 { return atomic.LoadInt32(&a.state) }

// SetStatus 设置状态。
func (a *agentImpl) SetStatus(state int32) { atomic.StoreInt32(&a.state, state) }

// SetLastAt 更新最后活跃时间。
func (a *agentImpl) SetLastAt() { atomic.StoreInt64(&a.lastAt, time.Now().Unix()) }

// Push 向客户端推送消息。
func (a *agentImpl) Push(route string, v interface{}) error {
	if a.GetStatus() == constants.StatusClosed {
		return errors.NewError(constants.ErrConnectionClosed, errors.PIT499)
	}
	return a.send(pendingMessage{typ: message.Push, route: route, payload: v})
}

// ResponseMID 向客户端回复消息。
func (a *agentImpl) ResponseMID(ctx context.Context, mid uint, v interface{}, isError ...bool) error {
	err := false
	if len(isError) > 0 {
		err = isError[0]
	}
	if a.GetStatus() == constants.StatusClosed {
		return errors.NewError(constants.ErrConnectionClosed, errors.PIT499)
	}
	return a.send(pendingMessage{ctx: ctx, typ: message.Response, mid: mid, payload: v, err: err})
}

// Close 关闭代理，释放资源。
func (a *agentImpl) Close() error {
	a.closeMutex.Lock()
	defer a.closeMutex.Unlock()

	if a.GetStatus() == constants.StatusClosed {
		return fmt.Errorf("会话已关闭")
	}
	a.SetStatus(constants.StatusClosed)

	logger.Debugw("会话关闭",
		"会话ID", a.Session.ID(),
		"用户", a.Session.UID(),
		"远程地址", a.conn.RemoteAddr().String(),
	)

	select {
	case <-a.chDie:
	default:
		close(a.chStopWrite)
		close(a.chStopHeartbeat)
		close(a.chDie)
	}

	// 执行会话关闭回调
	a.Session.Close()

	return a.conn.Close()
}

// Kick 发送踢下线包并关闭连接。
func (a *agentImpl) Kick(ctx context.Context) error {
	p, err := a.encoder.Encode(packet.Kick, nil)
	if err != nil {
		return fmt.Errorf("构建踢下线包失败: %w", err)
	}
	if err := a.writeToConnection(ctx, p); err != nil {
		logger.Debugw("发送踢下线包失败", "错误", err)
	}
	return a.Close()
}

// Handle 启动心跳和写协程，阻塞直到连接关闭。
func (a *agentImpl) Handle() {
	defer func() {
		a.Close()
		logger.Debugw("Agent handle 退出", "会话ID", a.Session.ID(), "用户", a.Session.UID())
	}()
	go a.write()
	go a.heartbeat()
	<-a.chDie
}

// SendHandshakeResponse 发送握手成功响应。
func (a *agentImpl) SendHandshakeResponse() error {
	_, err := a.conn.Write(hrd)
	return err
}

// SendHandshakeErrorResponse 发送握手失败响应。
func (a *agentImpl) SendHandshakeErrorResponse() error {
	_, err := a.conn.Write(herd)
	return err
}

// AnswerWithError 向客户端回复错误。
func (a *agentImpl) AnswerWithError(ctx context.Context, mid uint, err error) {
	err = errors.NewError(err, errors.PIT499)
	payload, errMarshal := json.Marshal(map[string]string{"msg": err.Error()})
	if errMarshal != nil {
		logger.Errorw("序列化错误消息失败", "错误", errMarshal)
		return
	}
	if e := a.ResponseMID(ctx, mid, payload, true); e != nil {
		logger.Errorw("发送错误回复失败", "错误", e)
	}
}

// ——— 内部方法 ———

func (a *agentImpl) send(pm pendingMessage) error {
	payload, err := serializeOrRaw(a.serializer, pm.payload)
	if err != nil {
		return err
	}

	m := &message.Message{
		Type:  pm.typ,
		Data:  payload,
		Route: pm.route,
		ID:    pm.mid,
		Err:   pm.err,
	}

	em, err := a.messageEncoder.Encode(m)
	if err != nil {
		return err
	}

	p, err := a.encoder.Encode(packet.Data, em)
	if err != nil {
		return err
	}

	pWrite := pendingWrite{ctx: pm.ctx, data: p}

	select {
	case a.chSend <- pWrite:
	case <-a.chDie:
	}

	return nil
}

func (a *agentImpl) write() {
	defer func() {
		a.Close()
	}()

	for {
		select {
		case pWrite := <-a.chSend:
			if err := a.writeToConnection(pWrite.ctx, pWrite.data); err != nil {
				logger.Errorw("写入连接失败，关闭代理",
					"远程地址", a.conn.RemoteAddr(),
					"用户", a.Session.UID(),
					"错误", err,
				)
				return
			}
		case <-a.chStopWrite:
			return
		}
	}
}

func (a *agentImpl) writeToConnection(ctx context.Context, data []byte) error {
	if a.writeTimeout > 0 {
		a.conn.SetWriteDeadline(time.Now().Add(a.writeTimeout))
	}
	_, err := a.conn.Write(data)
	return err
}

func (a *agentImpl) heartbeat() {
	ticker := time.NewTicker(a.heartbeatTimeout)
	defer func() {
		ticker.Stop()
		a.Close()
	}()

	for {
		select {
		case <-ticker.C:
			deadline := time.Now().Add(-2 * a.heartbeatTimeout).Unix()
			if atomic.LoadInt64(&a.lastAt) < deadline {
				logger.Debugw("心跳超时，关闭会话",
					"会话ID", a.Session.ID(),
					"用户", a.Session.UID(),
				)
				return
			}
			select {
			case a.chSend <- pendingWrite{data: hbd}:
			case <-a.chDie:
				return
			case <-a.chStopHeartbeat:
				return
			}
		case <-a.chDie:
			return
		case <-a.chStopHeartbeat:
			return
		}
	}
}

// ——— 编码辅助 ———

func serializeOrRaw(s serialize.Serializer, payload interface{}) ([]byte, error) {
	if d, ok := payload.([]byte); ok {
		return d, nil
	}
	return s.Marshal(payload)
}

func hbdEncode(heartbeatTimeout time.Duration, encoder codec.PacketEncoder, compressionEnabled bool, serializerName string) {
	hData := map[string]interface{}{
		"code": 200,
		"sys": map[string]interface{}{
			"heartbeat":  heartbeatTimeout.Seconds(),
			"dict":       message.GetDictionary(),
			"serializer": serializerName,
		},
	}

	var err error
	hrd, err = encodeHandshakeData(hData, encoder, compressionEnabled)
	if err != nil {
		panic(fmt.Sprintf("编码握手响应失败: %v", err))
	}

	hbd, err = encoder.Encode(packet.Heartbeat, nil)
	if err != nil {
		panic(fmt.Sprintf("编码心跳包失败: %v", err))
	}
}

func herdEncode(heartbeatTimeout time.Duration, encoder codec.PacketEncoder, compressionEnabled bool, serializerName string) {
	hErrData := map[string]interface{}{
		"code": 400,
		"sys": map[string]interface{}{
			"heartbeat":  heartbeatTimeout.Seconds(),
			"dict":       message.GetDictionary(),
			"serializer": serializerName,
		},
	}

	var err error
	herd, err = encodeHandshakeData(hErrData, encoder, compressionEnabled)
	if err != nil {
		panic(fmt.Sprintf("编码握手错误响应失败: %v", err))
	}
}

func encodeHandshakeData(data interface{}, encoder codec.PacketEncoder, compressionEnabled bool) ([]byte, error) {
	encData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	if compressionEnabled {
		compressed, err := compression.DeflateData(encData)
		if err != nil {
			return nil, err
		}
		if len(compressed) < len(encData) {
			encData = compressed
		}
	}

	return encoder.Encode(packet.Handshake, encData)
}
