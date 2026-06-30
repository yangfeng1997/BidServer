//go:build integration

package internal

import "testing"

// TestReconnectRejoin_EndToEnd 验证掉线 5min 内重连接回原拍卖局（需容器 NATS+etcd+MongoDB）。
// 流程：登录 L1 → 匹配开局 room X → 出价 → 掉线（in-game 保留在线条目）→
//
//	重连选 L2 → Login 查 online 拿 {X,g1} → RoomHandler.rejoin 改投 L2 + 回快照 →
//	重建亲和 + 收 SC_ReconnectAuction → 继续出价 → 收后续广播与结算（落点 L2）。
//
// 沙箱无 Docker（umbrella D10）：仅编译验证，实跑留 Docker host。
func TestReconnectRejoin_EndToEnd(t *testing.T) {
	t.Skip("integration: requires NATS+etcd+MongoDB container (no Docker in sandbox)")
}

// TestReconnect_RoomDeadVoided 验证 room 死/超 5min 窗 → 作废清亲和回大厅（mailbox/offline 兜结算）。
func TestReconnect_RoomDeadVoided(t *testing.T) {
	t.Skip("integration: requires NATS+etcd+MongoDB container (no Docker in sandbox)")
}
