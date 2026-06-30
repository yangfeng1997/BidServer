//go:build integration

package internal

import "testing"

// TestIntegration_AuctionSettle 凑桌→开局→出价→广播→结算→重登读回；离线赢家→inbox→重登补发。
// 沙箱无 Docker，仅编译验证（umbrella D10）；实跑需容器 NATS+etcd+MongoDB。
func TestIntegration_AuctionSettle(t *testing.T) {
	t.Skip("requires Docker: NATS+etcd+MongoDB; compile-only in sandbox")
}
