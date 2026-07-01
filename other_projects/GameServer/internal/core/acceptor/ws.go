package acceptor

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"project/internal/core/codec"
	"project/internal/core/conn"
)

// WebSocket 接入器
type WSAcceptor struct {
	addr     string
	path     string
	srv      *http.Server
	ch       chan conn.Connection
	done     chan struct{}
	once     sync.Once
	listen   sync.Once
	upgrader websocket.Upgrader
}

// 创建 WebSocket 接入器
func NewWSAcceptor(addr, path string) *WSAcceptor {
	if path == "" {
		path = "/ws"
	}
	return &WSAcceptor{
		addr: addr,
		path: path,
		ch:   make(chan conn.Connection, 64),
		done: make(chan struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
}

// Listen 启动 HTTP 服务器并注册 WebSocket 路由
func (a *WSAcceptor) Listen() error {
	if a.addr == "" {
		return errors.New("ws acceptor: empty listen addr")
	}
	var err error
	a.listen.Do(func() {
		mux := http.NewServeMux()
		mux.HandleFunc(a.path, a.handleWS)
		a.srv = &http.Server{
			Addr:    a.addr,
			Handler: mux,
		}
		ln, lerr := net.Listen("tcp", a.addr)
		if lerr != nil {
			err = lerr
			return
		}
		go func() {
			_ = a.srv.Serve(ln)
		}()
		go a.shutdownWatch()
	})
	return err
}

func (a *WSAcceptor) shutdownWatch() {
	<-a.done
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = a.srv.Shutdown(ctx)
}

// Accept 返回新连接通道
func (a *WSAcceptor) Accept() <-chan conn.Connection { return a.ch }

// Close 关闭接入器
func (a *WSAcceptor) Close() error {
	var err error
	a.once.Do(func() {
		close(a.done)
	})
	return err
}

func (a *WSAcceptor) handleWS(w http.ResponseWriter, r *http.Request) {
	wsConn, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	wrapped := &wsConnection{
		conn:   wsConn,
		done:   make(chan struct{}),
		recvCh: make(chan *codec.Packet, 64),
		sendCh: make(chan []byte, 256),
	}
	wrapped.TouchRecv()
	wrapped.remoteAddr = r.RemoteAddr
	go wrapped.writeLoop()
	go wrapped.readLoop()

	select {
	case a.ch <- wrapped:
	case <-a.done:
		_ = wrapped.Close()
	}
}

// WebSocket 连接适配器
type wsConnection struct {
	conn       *websocket.Conn
	remoteAddr string
	done       chan struct{}
	closeOnce  sync.Once
	lastRecv   atomic.Int64
	recvCh     chan *codec.Packet
	sendCh     chan []byte
}

func (c *wsConnection) Send(data []byte) {
	if len(data) == 0 {
		return
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	select {
	case <-c.done:
		return
	case c.sendCh <- buf:
	}
}

func (c *wsConnection) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		err = c.conn.Close()
	})
	return err
}

func (c *wsConnection) RemoteAddr() string {
	if c.remoteAddr != "" {
		return c.remoteAddr
	}
	return c.conn.RemoteAddr().String()
}

func (c *wsConnection) Done() <-chan struct{} { return c.done }

func (c *wsConnection) LastRecvUnixNano() int64 { return c.lastRecv.Load() }

func (c *wsConnection) TouchRecv() { c.lastRecv.Store(time.Now().UnixNano()) }

func (c *wsConnection) Recv() <-chan *codec.Packet { return c.recvCh }

func (c *wsConnection) writeLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case data, ok := <-c.sendCh:
			if !ok {
				return
			}
			// WebSocket 帧直接发送 binary message
			if err := c.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				_ = c.Close()
				return
			}
		case <-ticker.C:
			if err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				_ = c.Close()
				return
			}
		}
	}
}

func (c *wsConnection) readLoop() {
	defer close(c.recvCh)
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			_ = c.Close()
			return
		}
		c.TouchRecv()
		if len(raw) < 4 {
			continue
		}
		pkt, derr := codec.DecodePacket(raw)
		if derr != nil {
			continue
		}
		select {
		case c.recvCh <- &pkt:
		case <-c.done:
			return
		}
	}
}

// connID 生成一个随机连接标识符
func connID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	buf := make([]byte, 16)
	binary.BigEndian.PutUint64(buf, uint64(time.Now().UnixNano()))
	binary.BigEndian.PutUint64(buf[8:], binary.BigEndian.Uint64(b))
	return hex.EncodeToString(buf)
}

var _ conn.Connection = (*wsConnection)(nil)
var _ Acceptor = (*WSAcceptor)(nil)
