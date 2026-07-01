package routeragent

import (
	"fmt"
	"sync/atomic"
)

// Metrics 提供 RA 可观测性指标
type Metrics struct {
	PeerConnectTotal     atomic.Int64
	PeerConnectFailTotal atomic.Int64
	PeerDisconnectTotal  atomic.Int64
	ForwardTotal         atomic.Int64
	RemoteSeqTimeout     atomic.Int64
	LateResponse         atomic.Int64
	UnknownSeq           atomic.Int64
	PeerDisconnectDrop   atomic.Int64
	RouteMiss            atomic.Int64
	RemoteSeqPending     atomic.Int64 // Alloc +1, Pop -1
	WaiterCount          atomic.Int64
}

// 创建指标收集器
func NewMetrics() *Metrics { return &Metrics{} }

// 返回当前指标快照
func (m *Metrics) Snapshot() map[string]int64 {
	return map[string]int64{
		"peer_connect_total":      m.PeerConnectTotal.Load(),
		"peer_connect_fail_total": m.PeerConnectFailTotal.Load(),
		"peer_disconnect_total":   m.PeerDisconnectTotal.Load(),
		"forward_total":           m.ForwardTotal.Load(),
		"remote_seq_timeout":      m.RemoteSeqTimeout.Load(),
		"late_response":           m.LateResponse.Load(),
		"unknown_seq":             m.UnknownSeq.Load(),
		"peer_disconnect_drop":    m.PeerDisconnectDrop.Load(),
		"route_miss":              m.RouteMiss.Load(),
		"remote_seq_pending":      m.RemoteSeqPending.Load(),
		"waiter_count":            m.WaiterCount.Load(),
	}
}

// 返回指标的人类可读摘要
func (m *Metrics) String() string {
	s := m.Snapshot()
	return fmt.Sprintf(
		"RA Metrics: peers(connect:%d fail:%d dis:%d) fwd:%d seq(pending:%d timeout:%d late:%d unk:%d) route_miss:%d waiter:%d",
		s["peer_connect_total"], s["peer_connect_fail_total"], s["peer_disconnect_total"],
		s["forward_total"],
		s["remote_seq_pending"], s["remote_seq_timeout"],
		s["late_response"], s["unknown_seq"],
		s["route_miss"], s["waiter_count"],
	)
}
