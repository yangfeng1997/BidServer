package acceptor

// Acceptor 网络监听器接口，不同协议（TCP/WS）各自实现
type Acceptor interface {
	// ListenAndServe 开始监听并接受连接，阻塞直到 Stop 被调用
	ListenAndServe()
	// Stop 停止监听，关闭 ConnChan
	Stop()
	// ConnChan 返回连接通道，消费方通过 range 接收新连接
	ConnChan() chan ClientConn
	// IsRunning 返回监听器是否正在运行
	IsRunning() bool
}
