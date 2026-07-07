package routerclient

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	onlinepb "project/protocal/gen/online"
	routerpb "project/protocal/gen/router"
	"project/src/framework/cluster"
)

// fakeCluster 仅实现 CallAnySync，其余方法继承接口（不被调用）
type fakeCluster struct {
	cluster.Cluster
	gotType, gotRoute string
	gotReq            *routerpb.RPC_RouterForward_Req
	rsp               *routerpb.RPC_RouterForward_Rsp
}

func (f *fakeCluster) CallAnySync(_ context.Context, serverType, route string, req proto.Message) ([]byte, error) {
	f.gotType, f.gotRoute = serverType, route
	f.gotReq = req.(*routerpb.RPC_RouterForward_Req)
	return proto.Marshal(f.rsp)
}

func TestCallViaSync_WrapsAndUnwraps(t *testing.T) {
	innerRsp := &onlinepb.RPC_Register_Rsp{Code: 0, KickedOld: true}
	innerBytes, _ := proto.Marshal(innerRsp)
	fc := &fakeCluster{rsp: &routerpb.RPC_RouterForward_Rsp{Code: 0, InnerData: innerBytes}}

	out, err := CallViaSync[*onlinepb.RPC_Register_Rsp](
		context.Background(), fc, "onlinesvr",
		routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, "10001",
		"OnlineHandler.register",
		&onlinepb.RPC_Register_Req{Uid: 10001, GatewayNodeId: "1.1.1", LobbyNodeId: "1.2.1"},
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !out.KickedOld {
		t.Fatalf("inner rsp not unwrapped: %+v", out)
	}
	if fc.gotType != "routersvr" || fc.gotRoute != "RouterHandler.forward" {
		t.Fatalf("wrong dispatch target: %s/%s", fc.gotType, fc.gotRoute)
	}
	if fc.gotReq.TargetType != "onlinesvr" || fc.gotReq.RoutingKey != "10001" ||
		fc.gotReq.InnerRoute != "OnlineHandler.register" ||
		fc.gotReq.RoutingMode != routerpb.RoutingMode_ROUTING_CONSISTENT_HASH {
		t.Fatalf("envelope wrong: %+v", fc.gotReq)
	}
}

func TestCallViaSync_RouterError(t *testing.T) {
	fc := &fakeCluster{rsp: &routerpb.RPC_RouterForward_Rsp{Code: 1, ErrMsg: "no target"}}
	_, err := CallViaSync[*onlinepb.RPC_Register_Rsp](
		context.Background(), fc, "onlinesvr",
		routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, "1", "OnlineHandler.register",
		&onlinepb.RPC_Register_Req{Uid: 1})
	if err == nil {
		t.Fatal("expected error when router code != 0")
	}
}
