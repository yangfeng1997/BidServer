package routeragent

import (
	"sync"
	"time"
)

// incomingPeerSeqEntry 记录某个跨 RA 请求是从哪个 peer 连接入站的。
type incomingPeerSeqEntry struct {
	peerKey string
	at      time.Time
}

// IncomingPeerSeqStore 跟踪跨 RA 请求的入站 seq，用于响应回源时找到原 peer 连接。
// 它是 RouterAgent 的 state 层组件。
type IncomingPeerSeqStore struct {
	mu      sync.Mutex
	entries map[uint64]incomingPeerSeqEntry
}

// NewIncomingPeerSeqStore 创建入站 peer seq 跟踪器。
func NewIncomingPeerSeqStore() *IncomingPeerSeqStore {
	return &IncomingPeerSeqStore{entries: make(map[uint64]incomingPeerSeqEntry)}
}

// Track 记录一个 seq 的入站 peer。
func (s *IncomingPeerSeqStore) Track(seqID uint64, peerKey string) {
	if seqID == 0 || peerKey == "" {
		return
	}
	s.mu.Lock()
	s.entries[seqID] = incomingPeerSeqEntry{peerKey: peerKey, at: time.Now()}
	s.mu.Unlock()
}

// Pop 取出并删除一个 seq 的 peer 信息。
func (s *IncomingPeerSeqStore) Pop(seqID uint64) (peerKey string, ok bool) {
	s.mu.Lock()
	entry, has := s.entries[seqID]
	if has {
		delete(s.entries, seqID)
	}
	s.mu.Unlock()
	return entry.peerKey, has
}

// CleanupByPeer 清理指定 peer 的全部入站 seq。
func (s *IncomingPeerSeqStore) CleanupByPeer(peerKey string) {
	if peerKey == "" {
		return
	}
	s.mu.Lock()
	for seqID, entry := range s.entries {
		if entry.peerKey == peerKey {
			delete(s.entries, seqID)
		}
	}
	s.mu.Unlock()
}

// CleanupExpired 清理过期的入站 seq。
func (s *IncomingPeerSeqStore) CleanupExpired(now time.Time, retention time.Duration) {
	s.mu.Lock()
	for seqID, entry := range s.entries {
		if now.Sub(entry.at) > retention {
			delete(s.entries, seqID)
		}
	}
	s.mu.Unlock()
}

// Len 返回当前跟踪的入站 seq 数量。
func (s *IncomingPeerSeqStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
