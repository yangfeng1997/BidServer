package acceptor

import (
	"io"

	"github.com/xtaci/kcp-go/v5"

	"projectbid/server/conn/codec"
	"projectbid/server/logger"
)

// KCPAcceptor 实现基于 KCP 协议（UDP 可靠传输）的网络监听，适合移动弱网场景。
type KCPAcceptor struct {
	opts     Options
	listener *kcp.Listener
	connChan chan PlayerConn
	running  bool
}

// NewKCPAcceptor 创建 KCP 监听器。
func NewKCPAcceptor(opts Options) *KCPAcceptor {
	return &KCPAcceptor{
		opts:     opts,
		connChan: make(chan PlayerConn, opts.MessagesBufferSize),
	}
}

// GetAddr 返回监听地址。
func (a *KCPAcceptor) GetAddr() string {
	if a.listener != nil {
		return a.listener.Addr().String()
	}
	return ""
}

// GetConnChan 返回新连接通道。
func (a *KCPAcceptor) GetConnChan() <-chan PlayerConn {
	return a.connChan
}

// ListenAndServe 开始监听。
func (a *KCPAcceptor) ListenAndServe() error {
	addr := trimScheme(a.opts.Addr)

	// 默认不启用 FEC，由业务按需通过 KCPOptions 配置
	dataShards := 0
	parityShards := 0
	if kcpOpts, ok := a.opts.KCPOptions.(*KCPOptions); ok && kcpOpts != nil {
		dataShards = kcpOpts.DataShards
		parityShards = kcpOpts.ParityShards
	}

	var err error
	a.listener, err = kcp.ListenWithOptions(addr, nil, dataShards, parityShards)
	if err != nil {
		return err
	}
	a.running = true
	logger.Infow("KCP 监听器已启动", "地址", addr, "FEC数据分片", dataShards, "FEC冗余分片", parityShards)

	go a.serve()
	return nil
}

// Stop 停止监听。
func (a *KCPAcceptor) Stop() error {
	a.running = false
	if a.listener != nil {
		return a.listener.Close()
	}
	return nil
}

func (a *KCPAcceptor) serve() {
	defer func() {
		if a.listener != nil {
			a.listener.Close()
		}
	}()

	for a.running {
		conn, err := a.listener.AcceptKCP()
		if err != nil {
			if a.running {
				logger.Errorw("接受 KCP 连接失败", "错误", err)
			}
			continue
		}
		a.connChan <- newKCPConn(conn, a.opts.PacketDecoder)
	}
}

// ——— kcpConn ———

type kcpConn struct {
	*kcp.UDPSession
	decoder codec.PacketDecoder
}

func newKCPConn(conn *kcp.UDPSession, decoder codec.PacketDecoder) *kcpConn {
	return &kcpConn{
		UDPSession: conn,
		decoder:    decoder,
	}
}

// GetNextMessage 从 KCP 流中读取经过 Pomelo 编码的完整消息。
func (c *kcpConn) GetNextMessage() ([]byte, error) {
	header, err := io.ReadAll(io.LimitReader(c.UDPSession, codec.HeadLength))
	if err != nil {
		return nil, err
	}
	if len(header) == 0 {
		return nil, io.EOF
	}
	if len(header) < codec.HeadLength {
		return nil, io.ErrUnexpectedEOF
	}

	msgSize, _, err := codec.ParseHeader(header)
	if err != nil {
		return nil, err
	}

	msgData, err := io.ReadAll(io.LimitReader(c.UDPSession, int64(msgSize)))
	if err != nil {
		return nil, err
	}
	if len(msgData) < msgSize {
		return nil, io.ErrUnexpectedEOF
	}

	return append(header, msgData...), nil
}

// ——— KCPOptions ———

// KCPOptions KCP 特定配置，通过 Options.KCPOptions 注入。
type KCPOptions struct {
	// DataShards FEC 数据分片数（0 = 不启用 FEC）。
	DataShards int
	// ParityShards FEC 冗余分片数（0 = 不启用 FEC）。
	ParityShards int
}
