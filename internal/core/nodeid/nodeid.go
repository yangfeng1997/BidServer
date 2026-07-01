package nodeid

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	worldBits   = 16
	serverBits  = 8
	indexBits   = 8
	serverShift = indexBits
	worldShift  = indexBits + serverBits
	worldMask   = (1 << worldBits) - 1
	serverMask  = (1 << serverBits) - 1
	indexMask   = (1 << indexBits) - 1
)

// NodeID 表示节点编号
// 底层使用 uint32 编码 world serverType 和 serverIndex
// 点分格式仅用于日志和调试
type NodeID uint32

// Encode 将 world serverType serverIndex 编码成 NodeID
func Encode(world, serverType, index uint32) NodeID {
	return NodeID(((world & worldMask) << worldShift) |
		((serverType & serverMask) << serverShift) |
		(index & indexMask))
}

// Decode 将 NodeID 拆回 world serverType serverIndex
func Decode(id uint32) (world, serverType, index uint32) {
	world = (id >> worldShift) & worldMask
	serverType = (id >> serverShift) & serverMask
	index = id & indexMask
	return
}

// Uint32 返回原始编码
func (id NodeID) Uint32() uint32 { return uint32(id) }

// 返回 world.serverType.serverIndex 格式
func (id NodeID) String() string { return String(uint32(id)) }

// 返回 world.serverType.serverIndex 格式
func String(id uint32) string {
	world, serverType, index := Decode(id)
	return fmt.Sprintf("%d.%d.%d", world, serverType, index)
}

// 解析 world.serverType.serverIndex 格式
func Parse(s string) (NodeID, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return 0, fmt.Errorf("nodeid: invalid format %q", s)
	}
	world, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return 0, fmt.Errorf("nodeid: parse world: %w", err)
	}
	serverType, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return 0, fmt.Errorf("nodeid: parse server type: %w", err)
	}
	index, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil {
		return 0, fmt.Errorf("nodeid: parse server index: %w", err)
	}
	return Encode(uint32(world), uint32(serverType), uint32(index)), nil
}
