package agent

import (
	"strings"
	"time"

	"project/pkg/logger"
)

// MetricsSnapshot 返回当前 RouterAgent 指标快照，包含 peer queue 水位。
func (m *Runtime) MetricsSnapshot() map[string]int64 {
	out := m.metrics.Snapshot()
	out["incoming_peer_seq"] = int64(m.incomingSeq.Len())
	out["remote_seq_pending_current"] = m.remoteSeq.PendingLen()
	for _, peer := range m.peerMgr.List() {
		link, ok := peer.Link.(*tcpPeerLink)
		if !ok || link == nil {
			continue
		}
		key := sanitizeMetricKey(peer.Addr)
		out["peer_"+key+"_send_len"] = int64(len(link.sendCh))
		out["peer_"+key+"_send_cap"] = int64(cap(link.sendCh))
		out["peer_"+key+"_send_max"] = link.maxSendLen.Load()
		out["peer_"+key+"_prio_len"] = int64(len(link.prioSendCh))
		out["peer_"+key+"_prio_cap"] = int64(cap(link.prioSendCh))
		out["peer_"+key+"_prio_max"] = link.maxPrioLen.Load()
	}
	return out
}

func sanitizeMetricKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func (m *Runtime) logQueueStatsIfActive() {
	active := false
	for _, peer := range m.peerMgr.List() {
		link, ok := peer.Link.(*tcpPeerLink)
		if !ok || link == nil {
			continue
		}
		sendLen := len(link.sendCh)
		prioLen := len(link.prioSendCh)
		if sendLen == 0 && prioLen == 0 {
			continue
		}
		active = true
		logger.Info("routeragent peer queue stats",
			logger.String("peer_key", peer.Addr),
			logger.String("direction", peer.Direction),
			logger.Int("send_len", sendLen),
			logger.Int("send_cap", cap(link.sendCh)),
			logger.Int64("send_max", link.maxSendLen.Load()),
			logger.Int("prio_len", prioLen),
			logger.Int("prio_cap", cap(link.prioSendCh)),
			logger.Int64("prio_max", link.maxPrioLen.Load()))
	}
	incomingSeq := m.incomingSeq.Len()
	remoteSeqPending := m.remoteSeq.PendingLen()
	if !active && incomingSeq == 0 && remoteSeqPending == 0 {
		return
	}
	logger.Info("routeragent state stats",
		logger.Int64("remote_seq_pending", remoteSeqPending),
		logger.Int("incoming_peer_seq", incomingSeq))
}

func (m *Runtime) runQueueStatsLog(stop <-chan struct{}) {
	ticker := time.NewTicker(queueStatsLogEvery)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			m.logQueueStatsIfActive()
		}
	}
}
