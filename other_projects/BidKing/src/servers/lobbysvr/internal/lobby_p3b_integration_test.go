//go:build integration

package internal

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"google.golang.org/protobuf/proto"

	lobbypb "project/protocal/gen/lobby"
	"project/src/common/mongo"
)

// 本文件遵循 lobby_ec_integration_test.go 的 skip-stub 约定：
// 在 integration 构建标签下编译，引用真实的 P3b 类型/方法（让集成面被类型检查），
// 沙箱无容器，故统一 t.Skip；待 NATS+etcd+MongoDB 就绪时去掉 Skip 并按 doc-comment 补连接装配与断言。
// 具体连接/装配代码参考 login_integration_test.go（NewNatsCluster + app.Start 注入 handler + Runtime）。

// 流程：① 登录拿到 uid；② gainCurrency 给币（含 op-id 去重）；
// ③ purchase（原子扣币 + 加道具）→ SC_Purchase；重发同 op 的 purchase 不双扣不双加（幂等）；
// ④ 断连触发 flush（或缩短 FlushInterval）后，直接读 Mongo players 文档校验 currency/bag 落库；
// ⑤ 模拟重登（新 Runtime，同 uid，从 Mongo 加载）→ CS_CurrencyQuery 读回余额一致、背包道具一致。
//
// 关键断言点（务必覆盖）：
//   - CS_Purchase{op1,...} → SC_Purchase{Code:0}，扣币额=单价、加道具数正确
//   - CS_Purchase{op1,...} 重发 → SC_Purchase 幂等（余额/道具不变）
//   - flush 后 mongo FindByID(players,uid) 的 currency.balances 与 bag.items 与内存一致
//   - 新 Runtime 同 uid 登录 → CS_CurrencyQuery 读回余额一致
func TestIntegration_PurchasePersistsAndReloads(t *testing.T) {
	t.Skip("需要容器 NATS+etcd+MongoDB；沙箱仅编译验证。实跑去掉 Skip 并补连接装配。")
	_ = context.Background
	_ = time.Second
	_ = proto.Marshal
	_ = (*lobbypb.CS_Purchase)(nil)
	_ = (*lobbypb.SC_Purchase)(nil)
	_ = (*lobbypb.CS_CurrencyQuery)(nil)
	_ = NewMongoStore
	_ = NewMongoMailStore
	_ = mongo.Connect
	_ = CurrencyState{}
	_ = PlayerDoc{}
}

// 流程：① 通过 mailbox 给离线收件人插一封普通邮件（带附件）；
// ② 收件人登录；③ CS_MailList 能看到该邮件；
// ④ CS_MailClaim 领取成功并发放附件（背包/货币）；重复 CS_MailClaim 同一邮件失败（原子 claim 去重）。
//
// 关键断言点（务必覆盖）：
//   - Insert MailDoc{To:uid, Type:MailTypeNormal, Attachments:[...]} 落库
//   - CS_MailList → 列表含该邮件（取出其 ID.Hex() 作为 claim 入参）
//   - CS_MailClaim{id} → SC_MailClaim{Code:0}，附件入账
//   - CS_MailClaim{id} 重发 → SC_MailClaim 非成功码（claimed 已置，不重复发放）
func TestIntegration_OfflineMailDeliveryAndClaim(t *testing.T) {
	t.Skip("需要容器 NATS+etcd+MongoDB；沙箱仅编译验证。实跑去掉 Skip 并补连接装配。")
	_ = context.Background
	_ = time.Second
	_ = MailDoc{}
	_ = Attachment{}
	_ = MailTypeNormal
	_ = NewMongoMailStore
	_ = (*lobbypb.CS_MailList)(nil)
	_ = (*lobbypb.CS_MailClaim)(nil)
	_ = (*lobbypb.SC_MailClaim)(nil)
	_ = primitive.ObjectID{}.Hex
}

// 流程（好友握手最终一致）：
// ① A 登录后 CS_FriendAdd 加 B → 给 B 投递一封 friend_req 邮件；
// ② B 登录看到 friend_req，CS_FriendRespond 接受 → B 好友表加 A，并给 A 回投 friend_accept 邮件；
// ③ A 重登时 accept-scan（PendingFriendAccepts）消费 friend_accept → A 好友表加 B，双向建立。
//
// 关键断言点（务必覆盖）：
//   - CS_FriendAdd{A→B} → mailbox 出现 MailTypeFriendReq{To:B,From:A}
//   - B CS_FriendRespond{accept} → SC_FriendRespond{Code:0}，B.Friend 含 A，mailbox 出现 MailTypeFriendAccept{To:A,From:B}
//   - A 重登 accept-scan 后 A.Friend 含 B（最终一致，双向）
func TestIntegration_FriendHandshakeEventualConsistency(t *testing.T) {
	t.Skip("需要容器 NATS+etcd+MongoDB；沙箱仅编译验证。实跑去掉 Skip 并补连接装配。")
	_ = context.Background
	_ = time.Second
	_ = (*lobbypb.CS_FriendAdd)(nil)
	_ = (*lobbypb.CS_FriendRespond)(nil)
	_ = (*lobbypb.SC_FriendRespond)(nil)
	_ = MailTypeFriendReq
	_ = MailTypeFriendAccept
}
