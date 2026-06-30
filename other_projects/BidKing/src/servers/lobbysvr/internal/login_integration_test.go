//go:build integration

package internal

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	lobbypb "project/protocal/gen/lobby"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
)

// 端到端集成测试：真实 NATS+etcd 下，验证 lobbysvr 注册可被发现、其
// LobbyHandler.login 经集群 RPC 可达、proto 编解码端到端正确。
// gateway 的登录逻辑已在 gatesvr 单测覆盖，此处不重复 TCP/握手层。
//
// 与本服务实现同包（package internal），以直接构造真实的 LobbyHandler /
// LobbyModule —— Go 的 internal 包可见性禁止从 test/integration 外部导入。
//
// 运行：
//
//	docker compose -f test/docker-compose.yaml up -d
//	go test -tags integration ./src/servers/lobbysvr/internal/ -run TestLoginRPC_EndToEnd -v

var (
	etcdEndpoints = []string{"localhost:2379"}
	natsURLs      = []string{"nats://localhost:4222"}
)

func TestLoginRPC_EndToEnd(t *testing.T) {
	// 1. 起 lobbysvr（节点 1.2.1）
	lobbyID, err := cluster.ParseNodeID("1.2.1")
	if err != nil {
		t.Fatalf("parse lobby id: %v", err)
	}
	lobbyCls, err := transport.NewNatsCluster(lobbyID, transport.NatsClusterConfig{
		EtcdEndpoints:  etcdEndpoints,
		NatsURLs:       natsURLs,
		SelfAddr:       "127.0.0.1:8801",
		ServerTypeName: "lobbysvr",
	})
	if err != nil {
		t.Fatalf("lobby cluster: %v", err)
	}
	lobbyApp := application.NewBuilder().
		NodeID("1.2.1").
		NodeType("lobbysvr").
		Serializer("protobuf", protobuf.NewSerializer()).
		Cluster(lobbyCls).
		Build()
	// 用 fakeStore 构造 Runtime：本测试聚焦集群 RPC 可达 + 延迟回包，
	// 不验证持久化（背包落库见 lobby_ec_integration_test.go）。
	rt := NewRuntime(RuntimeConfig{NodeID: "1.2.1", Cluster: lobbyApp.Cluster(), Store: newFakeStore()})
	lobbyApp.Register(NewLobbyModule(rt))
	if err := lobbyApp.RegisterHandler(NewLobbyHandler(rt), nil); err != nil {
		t.Fatalf("register lobby handler: %v", err)
	}
	lobbyApp.Start()
	if err := lobbyCls.Init(); err != nil {
		t.Fatalf("lobby cluster init: %v", err)
	}
	defer lobbyCls.Stop()
	defer rt.Stop()

	// 2. 起测试客户端集群节点（模拟 gateway，节点 1.1.250）
	clientID, err := cluster.ParseNodeID("1.1.250")
	if err != nil {
		t.Fatalf("parse client id: %v", err)
	}
	clientCls, err := transport.NewNatsCluster(clientID, transport.NatsClusterConfig{
		EtcdEndpoints:  etcdEndpoints,
		NatsURLs:       natsURLs,
		SelfAddr:       "127.0.0.1:8899",
		ServerTypeName: "gatesvr",
	})
	if err != nil {
		t.Fatalf("client cluster: %v", err)
	}
	if err := clientCls.Init(); err != nil {
		t.Fatalf("client cluster init: %v", err)
	}
	defer clientCls.Stop()

	// 3. 等待发现 lobbysvr 注册
	if !waitForType(clientCls, "lobbysvr", 5*time.Second) {
		t.Fatal("lobbysvr not discovered within timeout")
	}

	// 4. 经集群 RPC 调 login，断言响应
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	data, err := clientCls.CallAnySync(ctx, "lobbysvr", "LobbyHandler.login",
		&lobbypb.RPC_Login_Req{Token: "valid", Platform: "ios"})
	if err != nil {
		t.Fatalf("call login: %v", err)
	}
	var rsp lobbypb.RPC_Login_Rsp
	if err := proto.Unmarshal(data, &rsp); err != nil {
		t.Fatalf("unmarshal rsp: %v", err)
	}
	if rsp.Code != 0 || rsp.Uid != 10001 || rsp.LobbyNodeId != "1.2.1" {
		t.Fatalf("unexpected rsp: %+v", &rsp)
	}
}

func waitForType(c *transport.NatsCluster, typeName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(c.Discovery().ByType(typeName)) > 0 {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
