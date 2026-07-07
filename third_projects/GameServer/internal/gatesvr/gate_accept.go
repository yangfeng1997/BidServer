package gatesvr

import (
	"project/internal/core/conn"
)

// 监听新连接
func (m *Module) acceptLoop() {
	if m.acceptor == nil {
		return
	}
	for {
		select {
		case <-m.stopCh:
			return
		case c, ok := <-m.acceptor.Accept():
			if !ok {
				return
			}
			m.poster.Post(func() {
				m.sessions.OnConnect(c)
			})
			go m.readConn(c)
		}
	}
}

// 从连接读取数据包并投递到主循环
func (m *Module) readConn(c conn.Connection) {
	for pkt := range c.Recv() {
		pkt := pkt
		m.poster.Post(func() {
			if err := m.dispatcher.HandlePacket(c, pkt); err != nil {
				_ = err
			}
		})
	}
	m.poster.Post(func() {
		m.sessions.OnDisconnect(c)
		m.pending.DeleteByConn(c)
	})
	_ = c.Close()
}
