package cluster

import (
	"fmt"
	"strconv"
	"strings"
)

// NodeID 32位节点标识，位划分：| 16位 worldID | 8位 serverTypeID | 8位 serverIndex |
//
// worldID=0 保留给未来跨 world 的全局服务（当前单 world MVP 未使用）
// worldID≥1 为具体游戏区的服务（当前所有服务部署在 world 1）
type NodeID uint32

// MakeNodeID 构造 NodeID
func MakeNodeID(worldID uint16, serverType uint8, index uint8) NodeID {
	return NodeID(uint32(worldID)<<16 | uint32(serverType)<<8 | uint32(index))
}

// ParseNodeID 从点分字符串解析 NodeID，如 "1.3.1"
func ParseNodeID(s string) (NodeID, error) {
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid nodeID format: %q", s)
	}
	worldID, err := strconv.ParseUint(parts[0], 10, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid worldID in %q: %w", s, err)
	}
	serverType, err := strconv.ParseUint(parts[1], 10, 8)
	if err != nil {
		return 0, fmt.Errorf("invalid serverType in %q: %w", s, err)
	}
	index, err := strconv.ParseUint(parts[2], 10, 8)
	if err != nil {
		return 0, fmt.Errorf("invalid serverIndex in %q: %w", s, err)
	}
	return MakeNodeID(uint16(worldID), uint8(serverType), uint8(index)), nil
}

func (n NodeID) WorldID() uint16    { return uint16(n >> 16) }
func (n NodeID) ServerType() uint8  { return uint8(n >> 8) }
func (n NodeID) ServerIndex() uint8 { return uint8(n) }

// String 点分格式：{worldID}.{serverTypeID}.{serverIndex}
func (n NodeID) String() string {
	return fmt.Sprintf("%d.%d.%d", n.WorldID(), n.ServerType(), n.ServerIndex())
}

// Subject 即 NATS subject
func (n NodeID) Subject() string { return n.String() }

// Uint32 返回原始32位值
func (n NodeID) Uint32() uint32 { return uint32(n) }

// WorldIDGlobal 全局保留 worldID（跨 world 服务使用）
const WorldIDGlobal uint16 = 0
