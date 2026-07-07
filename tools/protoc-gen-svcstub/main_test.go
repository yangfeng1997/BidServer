package main

import "testing"

func TestLintMethodName(t *testing.T) {
	tests := []struct {
		name        string
		methodName  string
		inputName   string
		outputName  string
		notify      bool
		kind        serviceKind
		expectError bool
	}{
		{
			name: "valid Notify", methodName: "TestNtf", inputName: "RPC_Test_Ntf", outputName: "",
			notify: true, kind: kindBackend, expectError: false,
		},
		{
			name: "valid Req/Rsp (FRONTEND)", methodName: "Ping", inputName: "CS_Ping_Req", outputName: "SC_Pong_Rsp",
			notify: false, kind: kindFrontend, expectError: false,
		},
		{
			name: "valid Req/Rsp (BACKEND)", methodName: "Test", inputName: "RPC_Test_Req", outputName: "RPC_Test_Rsp",
			notify: false, kind: kindBackend, expectError: false,
		},
		{
			name: "Notify missing _Ntf suffix", methodName: "TestNtf", inputName: "RPC_Test_Req", outputName: "",
			notify: true, kind: kindBackend, expectError: true,
		},
		{
			name: "Req missing _Req suffix", methodName: "Ping", inputName: "CS_Ping", outputName: "SC_Pong_Rsp",
			notify: false, kind: kindFrontend, expectError: true,
		},
		{
			name: "Rsp missing _Rsp suffix", methodName: "Ping", inputName: "CS_Ping_Req", outputName: "SC_Pong",
			notify: false, kind: kindFrontend, expectError: true,
		},
		{
			name: "FRONTEND input ending with _Rsp rejected", methodName: "Foo", inputName: "SC_Foo_Rsp", outputName: "CS_Foo_Req",
			notify: false, kind: kindFrontend, expectError: true,
		},
		{
			name: "BACKEND input with _Rsp allowed", methodName: "Foo", inputName: "SC_Foo_Rsp", outputName: "CS_Foo_Req",
			notify: false, kind: kindBackend, expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := lintMethodName(tt.methodName, tt.inputName, tt.outputName, tt.notify, tt.kind)
			gotErr := err != nil
			if gotErr != tt.expectError {
				t.Errorf("lintMethodName() error=%v, expectError=%v, err=%v", gotErr, tt.expectError, err)
			}
		})
	}
}

func TestServerTypeToConst(t *testing.T) {
	tests := map[string]string{
		"ST_GATESVR":     "serverTypeGate",
		"ST_LOBBYSVR":    "serverTypeLobby",
		"ST_ROUTERAGENT": "serverTypeRouterAgent",
	}
	for input, expected := range tests {
		if got := serverTypeToConst(input); got != expected {
			t.Errorf("serverTypeToConst(%q)=%q, want %q", input, got, expected)
		}
	}
}

func TestRouteConstName(t *testing.T) {
	if got := routeConstName("LobbyHandler", "Ping"); got != "RouteLobbyHandlerPing" {
		t.Fatalf("routeConstName=%q, want RouteLobbyHandlerPing", got)
	}
}

func TestToSnake(t *testing.T) {
	tests := map[string]string{
		"Lobby":     "lobby",
		"Online":    "online",
		"Room":      "room",
		"Match":     "match",
		"Gate":      "gate",
		"FooBar":    "foo_bar",
		"FooBARBaz": "foo_bar_baz",
	}
	for input, expected := range tests {
		if got := toSnake(input); got != expected {
			t.Errorf("toSnake(%q)=%q, want %q", input, got, expected)
		}
	}
}

func TestLastSeg(t *testing.T) {
	tests := map[string]string{
		"project/protocol/handler": "handler",
		"project/protocol/remote":  "remote",
		"no_slash":                 "no_slash",
	}
	for input, expected := range tests {
		if got := lastSeg(input); got != expected {
			t.Errorf("lastSeg(%q)=%q, want %q", input, got, expected)
		}
	}
}
