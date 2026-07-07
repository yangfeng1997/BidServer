package acceptor

import (
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"project/src/common/logger"
	"project/src/framework/network/packet"
)

const (
	wsReadBufSize    = 4096
	wsWriteBufSize   = 4096
	wsMaxMessageSize = 4 * 1024 * 1024 // 4MB
	wsWriteWait      = 10 * time.Second
)

// WSOption WSAcceptor 的配置选项
type WSOption func(*WSAcceptor)

// WithWSCert 启用 TLS，提供证书和私钥路径
func WithWSCert(certFile, keyFile string) WSOption {
	return func(a *WSAcceptor) {
		a.certFile = certFile
		a.keyFile = keyFile
	}
}

// WithWSCheckOrigin 自定义跨域检查，默认允许所有来源
func WithWSCheckOrigin(fn func(r *http.Request) bool) WSOption {
	return func(a *WSAcceptor) { a.checkOrigin = fn }
}

// WithWSConnChanSize 设置连接 channel 缓冲区大小，默认 64
func WithWSConnChanSize(size int) WSOption {
	return func(a *WSAcceptor) { a.connChan = make(chan ClientConn, size) }
}

// WithWSMaxMessageSize 设置单帧最大字节数，默认 4MB
func WithWSMaxMessageSize(size int64) WSOption {
	return func(a *WSAcceptor) { a.maxMsgSize = size }
}

// WSAcceptor 基于 gorilla/websocket 的 WebSocket acceptor
type WSAcceptor struct {
	addr        string
	certFile    string
	keyFile     string
	connChan    chan ClientConn
	checkOrigin func(r *http.Request) bool
	maxMsgSize  int64
	running     atomic.Bool
	server      atomic.Pointer[http.Server]
}

// NewWSAcceptor 创建 WSAcceptor
func NewWSAcceptor(addr string, opts ...WSOption) *WSAcceptor {
	a := &WSAcceptor{
		addr:       addr,
		connChan:   make(chan ClientConn, 64),
		maxMsgSize: wsMaxMessageSize,
		checkOrigin: func(r *http.Request) bool { return true },
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

var _ Acceptor = (*WSAcceptor)(nil)

func (a *WSAcceptor) ConnChan() chan ClientConn { return a.connChan }
func (a *WSAcceptor) IsRunning() bool              { return a.running.Load() }

// ListenAndServe 启动 WebSocket 监听，阻塞直到 Stop 被调用
func (a *WSAcceptor) ListenAndServe() {
	if !a.running.CompareAndSwap(false, true) {
		return
	}

	upgrader := &websocket.Upgrader{
		ReadBufferSize:  wsReadBufSize,
		WriteBufferSize: wsWriteBufSize,
		CheckOrigin:     a.checkOrigin,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(rw, r, nil)
		if err != nil {
			logger.Warn("ws upgrade failed", logger.Err(err))
			return
		}
		conn.SetReadLimit(a.maxMsgSize)
		wc := newWSClientConn(conn)
		select {
		case a.connChan <- wc:
		default:
			logger.Warn("ws connChan full, dropping connection")
			conn.Close()
		}
	})

	ln, err := a.makeListener()
	if err != nil {
		logger.Error("ws listen failed", logger.Err(err))
		a.running.Store(false)
		close(a.connChan)
		return
	}

	srv := &http.Server{Handler: mux}
	a.server.Store(srv)

	logger.Info("ws acceptor listening", logger.String("addr", a.addr))
	// Serve 在 listener 关闭后返回，不视为错误
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		logger.Error("ws serve error", logger.Err(err))
	}

	a.running.Store(false)
	close(a.connChan)
}

// Stop 优雅关闭 HTTP server
func (a *WSAcceptor) Stop() {
	if !a.running.CompareAndSwap(true, false) {
		return
	}
	srv := a.server.Load()
	if srv != nil {
		(*srv).Close()
	}
}

func (a *WSAcceptor) makeListener() (net.Listener, error) {
	ln, err := net.Listen("tcp", a.addr)
	if err != nil {
		return nil, err
	}
	if a.certFile != "" && a.keyFile != "" {
		cert, err := tls.LoadX509KeyPair(a.certFile, a.keyFile)
		if err != nil {
			ln.Close()
			return nil, err
		}
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	}
	return ln, nil
}

// --- wsClientConn ---

// wsClientConn 包装 gorilla/websocket.Conn，实现 ClientConn 接口
// gorilla 保证并发读安全（单 goroutine 读）和并发写安全（加锁），
// 我们额外用 sync.Once 保证 Close 幂等。
type wsClientConn struct {
	conn     *websocket.Conn
	once     sync.Once
	mu       sync.Mutex // gorilla 要求写操作串行化
	remoteAddr net.Addr
}

func newWSClientConn(conn *websocket.Conn) *wsClientConn {
	return &wsClientConn{
		conn:       conn,
		remoteAddr: conn.RemoteAddr(),
	}
}

// ReadPacket 读取一个完整的外层 Packet
// gorilla ReadMessage 天然是完整帧，不需要手动拼包
func (c *wsClientConn) ReadPacket() (*packet.Packet, error) {
	_, data, err := c.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if len(data) < 4 {
		return nil, packet.ErrInvalidPacket
	}
	t, n, err := packet.DecodeHeader(data)
	if err != nil {
		return nil, err
	}
	body := data[4:]
	if len(body) != n {
		return nil, packet.ErrInvalidPacket
	}
	return &packet.Packet{Type: t, Body: body}, nil
}

// Write 串行写入（gorilla 要求写不并发）
func (c *wsClientConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	err := c.conn.WriteMessage(websocket.BinaryMessage, b)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (c *wsClientConn) Close() error {
	var err error
	c.once.Do(func() { err = c.conn.Close() })
	return err
}

func (c *wsClientConn) RemoteAddr() net.Addr      { return c.remoteAddr }
func (c *wsClientConn) LocalAddr() net.Addr       { return c.conn.LocalAddr() }
func (c *wsClientConn) SetDeadline(t time.Time) error      { return c.conn.SetReadDeadline(t) }
func (c *wsClientConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *wsClientConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }

// Read 实现 net.Conn 接口（不应在主路径使用，WS 通过 ReadPacket 读帧）
func (c *wsClientConn) Read(b []byte) (int, error) {
	_, data, err := c.conn.ReadMessage()
	if err != nil {
		return 0, err
	}
	return copy(b, data), nil
}
