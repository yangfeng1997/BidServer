package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanRoutes(t *testing.T) {
	// 创建临时目录
	dir := t.TempDir()
	protoContent := `
syntax = "proto3";
package test;
import "google/protobuf/empty.proto";
import "protocol/common/options.proto";

message CS_Test_Req {
  option (protocol.common.cmd_id) = 3000;
}
message SC_Test_Rsp {
  option (protocol.common.cmd_id) = 3001;
}
message CS_Test_Ntf {
  option (protocol.common.cmd_id) = 3002;
  option (protocol.common.no_auth) = true;
}

service TestHandler {
  option (protocol.common.kind) = FRONTEND;
  option (protocol.common.server_type) = ST_LOBBYSVR;
  rpc Test(CS_Test_Req) returns (SC_Test_Rsp);
  rpc Notify(CS_Test_Ntf) returns (google.protobuf.Empty);
}
`
	protoPath := filepath.Join(dir, "test.proto")
	if err := os.WriteFile(protoPath, []byte(protoContent), 0o644); err != nil {
		t.Fatal(err)
	}

	routes, err := scanRoutes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}

	// 检查 Req/Rsp 条目
	found := false
	for _, r := range routes {
		if r.CmdID == "3000" {
			found = true
			if r.ServerType != "serverTypeLobby" {
				t.Errorf("expected serverTypeLobby, got %s", r.ServerType)
			}
			if r.Route != "TestHandler/Test" {
				t.Errorf("expected route TestHandler/Test, got %s", r.Route)
			}
			if r.RspCmdID != "3001" {
				t.Errorf("expected RspCmdID 3001, got %s", r.RspCmdID)
			}
		}
	}
	if !found {
		t.Error("route 3000 not found")
	}

	// 检查 Notify 条目
	found = false
	for _, r := range routes {
		if r.CmdID == "3002" {
			found = true
			if !r.NoAuth {
				t.Error("expected no_auth=true")
			}
			if r.RspCmdID != "0" {
				t.Errorf("expected RspCmdID 0 for notify, got %s", r.RspCmdID)
			}
		}
	}
	if !found {
		t.Error("route 3002 not found")
	}
}

func TestRenderRoutes(t *testing.T) {
	routes := []routeItem{
		{"3000", "serverTypeLobby", "TestHandler/Test", "3001", false},
		{"3002", "serverTypeLobby", "TestHandler/Notify", "0", true},
	}
	out := renderRoutes(routes)
	if out == "" {
		t.Fatal("renderRoutes returned empty string")
	}
	if !contains(out, "3000: {ServerType: serverTypeLobby, Route: \"TestHandler/Test\", RspCmdID: 3001}") {
		t.Error("missing expected route entry")
	}
	if !contains(out, "3002: true") {
		t.Error("missing expected auth whitelist entry")
	}
}

func TestServerTypeToConstGenRoutes(t *testing.T) {
	tests := map[string]string{
		"ST_GATESVR":     "serverTypeGate",
		"ST_LOBBYSVR":    "serverTypeLobby",
		"ST_ROOMSVR":     "serverTypeRoom",
		"ST_MATCHSVR":    "serverTypeMatch",
		"ST_ONLINESVR":   "serverTypeOnline",
		"ST_ROUTERAGENT": "serverTypeRouterAgent",
	}
	for input, expected := range tests {
		if got := serverTypeToConst(input); got != expected {
			t.Errorf("serverTypeToConst(%q)=%q, want %q", input, got, expected)
		}
	}
}

func TestScanRoutesRejectsCmdIDZero(t *testing.T) {
	dir := t.TempDir()
	badProto := `
syntax = "proto3";
package test;
import "google/protobuf/empty.proto";
import "protocol/common/options.proto";

message CS_Bad_Req {
  option (protocol.common.cmd_id) = 0;
}
message SC_Bad_Rsp {
  option (protocol.common.cmd_id) = 1;
}

service BadHandler {
  option (protocol.common.kind) = FRONTEND;
  option (protocol.common.server_type) = ST_LOBBYSVR;
  rpc Bad(CS_Bad_Req) returns (SC_Bad_Rsp);
}
`
	os.WriteFile(filepath.Join(dir, "bad.proto"), []byte(badProto), 0o644)
	_, err := scanRoutes(dir)
	if err == nil {
		t.Error("expected error for cmd_id=0")
	}
}

func TestScanRoutesRejectsDupCmdID(t *testing.T) {
	dir := t.TempDir()
	dupProto := `
syntax = "proto3";
package test;
import "google/protobuf/empty.proto";
import "protocol/common/options.proto";

message CS_A_Req { option (protocol.common.cmd_id) = 5000; }
message SC_A_Rsp { option (protocol.common.cmd_id) = 5001; }
message CS_B_Req { option (protocol.common.cmd_id) = 5000; }
message SC_B_Rsp { option (protocol.common.cmd_id) = 5002; }

service TestHandler {
  option (protocol.common.kind) = FRONTEND;
  option (protocol.common.server_type) = ST_LOBBYSVR;
  rpc A(CS_A_Req) returns (SC_A_Rsp);
  rpc B(CS_B_Req) returns (SC_B_Rsp);
}
`
	os.WriteFile(filepath.Join(dir, "dup.proto"), []byte(dupProto), 0o644)
	_, err := scanRoutes(dir)
	if err == nil {
		t.Error("expected error for duplicate cmd_id")
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
