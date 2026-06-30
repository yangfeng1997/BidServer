//go:build integration

package internal

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	lobbypb "project/protocal/gen/lobby"
	"project/src/common/mongo"
	"project/src/framework/cluster"
	clusterpb "project/src/framework/cluster/pb"
	"project/src/framework/cluster/transport"
)

// 流程：① 起真实 cluster（lobby 节点）+ Mongo 连接 + Runtime + LobbyHandler（经 registry 装入 cluster handler）。
// ② 用另一个 cluster 客户端（模拟 gate）CallSync 到 lobby：登录 → AddItem(op1) → AddItem(op1 重试) → BagList。
// ③ 断言：BagList 数量正确、op1 重试不双加；等待周期/断连 flush 后，从 Mongo 直接读 players 文档校验落库。
// ④ 模拟重登（新 Runtime，同 uid，从 Mongo 加载）→ BagList 读回一致。
//
// 具体连接/装配代码参考已落地的 onlinesvr/router *_integration_test.go（同款 NewNatsCluster + app.Start 注入 handler）。
// 关键断言点（务必覆盖）：
//   - CallSync(RPC_Login_Req, token="40004") → RPC_Login_Rsp{Uid:40004}
//   - CallSync(CS_AddItem{op1,item=100,count=5}) → SC_AddItem{count:5}
//   - CallSync(CS_AddItem{op1,item=100,count=5}) → SC_AddItem{count:5}（去重，不为 10）
//   - CallSync(CS_BagList) → SC_BagList 含 {100:5}
//   - 触发 flush（断连 RPC_PlayerDisconnect_Notify 或缩短 FlushInterval）后，mongo FindByID(players,40004) 的 bag.items["100"]==5
//   - 新 Runtime 同 uid 登录 → CS_BagList 读回 {100:5}

func TestLobbyEC_Login_Bag_Flush_Relogin(t *testing.T) {
	t.Skip("需要容器 NATS+etcd+MongoDB；沙箱仅编译验证。实跑去掉 Skip 并补连接装配。")
	_ = context.Background
	_ = time.Second
	_ = proto.Marshal
	_ = (*clusterpb.ClusterSession)(nil)
	_ = (*lobbypb.CS_AddItem)(nil)
	_ = mongo.Connect
	_ = cluster.WithSession
	_ = transport.NewNatsCluster
}
