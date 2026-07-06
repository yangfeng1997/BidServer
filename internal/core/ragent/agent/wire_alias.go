package agent

import "project/internal/core/ragent/wire"

type FrameType = wire.FrameType

const (
	FrameHandshake     = wire.FrameHandshake
	FrameHandshakeAck  = wire.FrameHandshakeAck
	FrameRpcRequest    = wire.FrameRpcRequest
	FrameRpcResponse   = wire.FrameRpcResponse
	FrameRpcNotify     = wire.FrameRpcNotify
	FrameHeartbeat     = wire.FrameHeartbeat
	FrameBroadcastSent = wire.FrameBroadcastSent
)

type Frame = wire.Frame

var (
	EncodeFrame     = wire.EncodeFrame
	AppendFrame     = wire.AppendFrame
	EncodeRPCFrame  = wire.EncodeRPCFrame
	DecodeFrame     = wire.DecodeFrame
	EncodeRouteBody = wire.EncodeRouteBody
	DecodeRouteBody = wire.DecodeRouteBody
)

type RPCWireHeader = wire.RPCWireHeader

var (
	EncodeRPCWireHeader = wire.EncodeRPCWireHeader
	RPCWireHeaderLen    = wire.RPCWireHeaderLen
	AppendRPCWireHeader = wire.AppendRPCWireHeader
	DecodeRPCWireHeader = wire.DecodeRPCWireHeader
)

type RoutingMode = wire.RoutingMode

const (
	RoutingModeAny       = wire.RoutingModeAny
	RoutingModeDirect    = wire.RoutingModeDirect
	RoutingModeHash      = wire.RoutingModeHash
	RoutingModeBroadcast = wire.RoutingModeBroadcast
)
