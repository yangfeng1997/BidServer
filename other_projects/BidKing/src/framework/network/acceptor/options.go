package acceptor

// TCPOption TCPAcceptor 的配置选项
type TCPOption func(*TCPAcceptor)

// WithAddress 设置监听地址
func WithAddress(addr string) TCPOption {
	return func(a *TCPAcceptor) {
		a.addr = addr
	}
}

// WithConnChanSize 设置连接通道缓冲区大小，默认 64
func WithConnChanSize(size int) TCPOption {
	return func(a *TCPAcceptor) {
		a.connChan = make(chan ClientConn, size)
	}
}
