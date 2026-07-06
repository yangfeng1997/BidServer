package routeragent

import (
	"errors"
	"fmt"
	"strconv"

	"project/pkg/logger"
)

// Router 是 RouterAgent 的 routing 层组件，负责帧路由决策和跨 RA 帧处理。
// 它不持有连接生命周期状态，只依赖 routing/state/peer 转发组件做决策。
type Router struct {
	memberTable *MemberTable
	resolver    *Resolver
	localConns  *LocalConnSet
	remoteSeq   *RemoteSeqMap
	peerFwd     *PeerForwarder
	incomingSeq *IncomingPeerSeqStore
	metrics     *Metrics
}

// NewRouter 创建路由器。
func NewRouter(memberTable *MemberTable, resolver *Resolver, localConns *LocalConnSet, remoteSeq *RemoteSeqMap, peerFwd *PeerForwarder, incomingSeq *IncomingPeerSeqStore, metrics *Metrics) *Router {
	return &Router{
		memberTable: memberTable,
		resolver:    resolver,
		localConns:  localConns,
		remoteSeq:   remoteSeq,
		peerFwd:     peerFwd,
		incomingSeq: incomingSeq,
		metrics:     metrics,
	}
}

// RouteFrame 路由本机业务进程发来的 RPC 帧。
func (r *Router) RouteFrame(c *UDSConn, frame Frame) {
	head, err := DecodeRPCWireHeader(frame.Header)
	if err != nil || len(frame.Header) == 0 {
		r.routeLegacyFrame(c, frame)
		return
	}

	switch frame.Type {
	case FrameRpcResponse:
		if head.DestNodeID != 0 {
			r.peerFwd.SendResponseViaPeer(head.SeqID, head.DestNodeID, frame)
			return
		}
		entry := r.remoteSeq.Pop(head.SeqID)
		if entry == nil || entry.udsConn == nil {
			return
		}
		head.SeqID = entry.origSeqID
		encoded := EncodeRPCWireHeader(head)
		_ = entry.udsConn.Send(Frame{Type: FrameRpcResponse, Header: encoded, Body: frame.Body})
	case FrameRpcRequest, FrameRpcNotify:
		r.metrics.ForwardTotal.Add(1)
		r.forwardRPC(c, frame, head)
	}
}

func (r *Router) forwardRPC(c *UDSConn, frame Frame, head RPCWireHeader) {
	r.metrics.ForwardTotal.Add(1)
	targets := r.pickTargets(head)
	origSeqID := head.SeqID
	if len(targets) == 0 {
		return
	}
	for _, nodeID := range targets {
		info, ok := r.memberTable.GetByNodeID(nodeID)
		if !ok {
			continue
		}
		if local := r.localConns.Get(nodeID); local != nil {
			out := frame
			head.DestNodeID = nodeID
			out.Header = EncodeRPCWireHeader(head)
			_ = local.Send(out)
			continue
		}
		_ = r.peerFwd.SendOrQueue(info.RAAddr, head.ServerType, peerOutbound{
			source:       c,
			frame:        frame,
			head:         head,
			origSeqID:    origSeqID,
			targetNodeID: nodeID,
			prepareRPC:   true,
		})
	}
}

func (r *Router) pickTargets(head RPCWireHeader) []uint32 {
	switch RoutingMode(head.RoutingMode) {
	case RoutingModeDirect:
		nodeID, err := parseNodeIDKey(head.RoutingKey)
		if err != nil {
			return nil
		}
		return []uint32{nodeID}
	case RoutingModeHash:
		node, ok := r.memberTable.PickHashByServerType(head.ServerType, head.RoutingKey)
		if !ok {
			return nil
		}
		return []uint32{node.NodeID}
	case RoutingModeBroadcast:
		return r.memberTable.ListNodeIDsByServerType(head.ServerType)
	default:
		node, ok := r.memberTable.PickAnyByServerType(head.ServerType)
		if !ok {
			return nil
		}
		return []uint32{node.NodeID}
	}
}

func (r *Router) routeLegacyFrame(c *UDSConn, frame Frame) {
	nodeID, payload, err := DecodeRouteBody(frame.Body)
	if err != nil {
		return
	}
	info, ok := r.memberTable.GetByNodeID(nodeID)
	if !ok {
		return
	}
	if local := r.localConns.Get(nodeID); local != nil {
		_ = local.Send(frame)
		return
	}
	_ = r.peerFwd.SendOrQueue(info.RAAddr, 0, peerOutbound{frame: Frame{Type: frame.Type, Body: EncodeRouteBody(nodeID, payload)}})
}

// HandlePeerFrame 处理从远端 peer 收到的帧。
func (r *Router) HandlePeerFrame(f Frame, peerKey string) {
	switch f.Type {
	case FrameRpcResponse:
		head, err := DecodeRPCWireHeader(f.Header)
		if err != nil {
			logger.Warn("routeragent peer response decode failed", logger.String("peer_key", peerKey), logger.Err(err))
			return
		}
		entry := r.remoteSeq.Pop(head.SeqID)
		if entry != nil && entry.udsConn != nil {
			head.SeqID = entry.origSeqID
			if err := entry.udsConn.Send(Frame{Type: FrameRpcResponse, Header: EncodeRPCWireHeader(head), Body: f.Body}); err != nil {
				logger.Warn("routeragent peer response send uds failed", logger.String("peer_key", peerKey), logger.Uint64("seq", head.SeqID), logger.Err(err))
			}
		} else {
			logger.Warn("routeragent peer response late or missing seq", logger.String("peer_key", peerKey), logger.Uint64("seq", head.SeqID))
			r.metrics.LateResponse.Add(1)
		}
		r.metrics.ForwardTotal.Add(1)
	case FrameRpcRequest, FrameRpcNotify:
		r.metrics.ForwardTotal.Add(1)
		head, err := DecodeRPCWireHeader(f.Header)
		if err != nil {
			logger.Warn("routeragent peer request decode failed", logger.String("peer_key", peerKey), logger.Err(err))
			return
		}
		nodeID := head.DestNodeID
		if nodeID == 0 {
			parsed, err := parseNodeIDKey(head.RoutingKey)
			if err != nil {
				r.metrics.RouteMiss.Add(1)
				return
			}
			nodeID = parsed
		}
		if f.Type == FrameRpcRequest {
			r.incomingSeq.Track(head.SeqID, peerKey)
		}
		r.deliverToLocal(nodeID, f)
	}
}

func (r *Router) deliverToLocal(nodeID uint32, f Frame) {
	if err := r.localConns.Deliver(nodeID, f); err != nil {
		logger.Warn("routeragent deliver local failed", logger.Uint32("node_id", nodeID), logger.Int("frame_type", int(f.Type)), logger.Err(err))
		r.metrics.RouteMiss.Add(1)
	}
}

// SendToNode 发送帧到指定节点，本机优先，否则走 peer 转发。
func (r *Router) SendToNode(nodeID uint32, frame Frame) error {
	if local := r.localConns.Get(nodeID); local != nil {
		return local.Send(frame)
	}
	info, ok := r.memberTable.GetByNodeID(nodeID)
	if !ok {
		return errors.New("node not found")
	}
	return r.peerFwd.SendOrQueue(info.RAAddr, 0, peerOutbound{frame: frame})
}

func parseNodeIDKey(key string) (uint32, error) {
	if key == "" {
		return 0, errors.New("empty node id key")
	}
	v, err := strconv.ParseUint(key, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid node id %q: %w", key, err)
	}
	return uint32(v), nil
}
