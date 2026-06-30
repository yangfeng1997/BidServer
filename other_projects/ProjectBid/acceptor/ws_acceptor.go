package acceptor

import (
	"io"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"projectbid/server/conn/codec"
	"projectbid/server/logger"
)

// WSAcceptor 实现 WebSocket 网络监听。
type WSAcceptor struct {
	opts     Options
	listener net.Listener
	connChan chan PlayerConn
	running  bool
}

// NewWSAcceptor 创建 WebSocket 监听器。
func NewWSAcceptor(opts Options) *WSAcceptor {
	return &WSAcceptor{
		opts:     opts,
		connChan: make(chan PlayerConn, opts.MessagesBufferSize),
	}
}

// GetAddr 返回监听地址。
func (a *WSAcceptor) GetAddr() string {
	if a.listener != nil {
		return a.listener.Addr().String()
	}
	return ""
}

// GetConnChan 返回新连接通道。
func (a *WSAcceptor) GetConnChan() <-chan PlayerConn {
	return a.connChan
}

// ListenAndServe 开始监听。
func (a *WSAcceptor) ListenAndServe() error {
	var err error
	a.listener, err = net.Listen("tcp", a.opts.Addr)
	if err != nil {
		return err
	}

	a.running = true
	logger.Infow("WebSocket 监听器已启动", "地址", a.opts.Addr)

	go a.serve()
	return nil
}

// Stop 停止监听。
func (a *WSAcceptor) Stop() error {
	a.running = false
	if a.listener != nil {
		return a.listener.Close()
	}
	return nil
}

func (a *WSAcceptor) serve() {
	defer func() {
		if a.listener != nil {
			a.listener.Close()
		}
	}()

	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}

	handler := &wsConnHandler{
		upgrader: &upgrader,
		connChan: a.connChan,
	}

	if err := http.Serve(a.listener, handler); err != nil {
		if a.running {
			logger.Errorw("WebSocket 服务失败", "错误", err)
		}
	}
}

type wsConnHandler struct {
	upgrader *websocket.Upgrader
	connChan chan PlayerConn
}

func (h *wsConnHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Errorw("WebSocket 升级失败", "错误", err)
		return
	}

	h.connChan <- newWSConn(conn)
}

// ——— wsConn ———

// wsConn 将 *websocket.Conn 适配为 net.Conn 接口。
type wsConn struct {
	conn   *websocket.Conn
	reader io.Reader
}

func newWSConn(conn *websocket.Conn) *wsConn {
	return &wsConn{conn: conn}
}

// GetNextMessage 从 WebSocket 连接读取完整消息。
func (c *wsConn) GetNextMessage() ([]byte, error) {
	_, msgBytes, err := c.conn.ReadMessage()
	if err != nil {
		return nil, err
	}

	if len(msgBytes) < codec.HeadLength {
		return nil, io.ErrUnexpectedEOF
	}

	header := msgBytes[:codec.HeadLength]
	msgSize, _, err := codec.ParseHeader(header)
	if err != nil {
		return nil, err
	}

	dataLen := len(msgBytes[codec.HeadLength:])
	if dataLen < msgSize {
		return nil, io.ErrUnexpectedEOF
	}

	return msgBytes, nil
}

// Read 从 WebSocket 读取数据。
func (c *wsConn) Read(b []byte) (int, error) {
	if c.reader == nil {
		_, r, err := c.conn.NextReader()
		if err != nil {
			return 0, err
		}
		c.reader = r
	}
	n, err := c.reader.Read(b)
	if err == io.EOF {
		_, r, nextErr := c.conn.NextReader()
		if nextErr != nil {
			return n, err
		}
		c.reader = r
		return n, nil
	}
	return n, err
}

// Write 向 WebSocket 写入二进制数据。
func (c *wsConn) Write(b []byte) (int, error) {
	err := c.conn.WriteMessage(websocket.BinaryMessage, b)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

// Close 关闭 WebSocket 连接。
func (c *wsConn) Close() error {
	return c.conn.Close()
}

// LocalAddr 返回本地地址。
func (c *wsConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

// RemoteAddr 返回远程地址。
func (c *wsConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

// SetDeadline 设置读写超时。
func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

// SetReadDeadline 设置读超时。
func (c *wsConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// SetWriteDeadline 设置写超时。
func (c *wsConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}
