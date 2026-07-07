//go:build integration

package internal

import (
	"testing"
)

// TestP4a_MatchToOpenGame 全链路：登录→发起匹配→凑桌→开局→lobby 拿 room 绑定→online 可见。
// 沙箱无 Docker 不实跑（go vet -tags integration 编译验证）；实跑需 NATS+JetStream+etcd+MongoDB。
func TestP4a_MatchToOpenGame(t *testing.T) {
	t.Skip("requires NATS+JetStream+etcd+MongoDB; sandbox compile-verify only")
	// 1. 起 router/online/lobby/room/match 五节点（startNode 模式，见 online_integration_test.go）
	// 2. mq := matchqueue.NewJetStreamQueue(natsURLs); 注入 router publisher + match consumer
	// 3. 两玩家登录 lobby（token="1001"/"1002"），各触发 LobbyHandler.startmatch
	// 4. 轮询：两玩家 lobby Player(uid).RoomAffinity() != nil（同 gameId/room）
	// 5. online RPC_Query：两 uid 的 Entry.RoomNodeId/GameId 一致且非空
}
