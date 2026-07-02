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
			name: "valid Notify", methodName: "SyncPos", inputName: "CS_SyncPos_Ntf", outputName: "",
			notify: true, kind: kindFrontend, expectError: false,
		},
		{
			name: "valid Req/Rsp (FRONTEND)", methodName: "ClaimReward", inputName: "CS_ClaimReward_Req", outputName: "SC_ClaimReward_Rsp",
			notify: false, kind: kindFrontend, expectError: false,
		},
		{
			name: "valid Req/Rsp (BACKEND)", methodName: "Login", inputName: "RPC_Login_Req", outputName: "RPC_Login_Rsp",
			notify: false, kind: kindBackend, expectError: false,
		},
		{
			name: "Notify missing _Ntf suffix", methodName: "SyncPos", inputName: "CS_SyncPos_Req", outputName: "",
			notify: true, kind: kindFrontend, expectError: true,
		},
		{
			name: "Req missing _Req suffix", methodName: "ClaimReward", inputName: "CS_ClaimReward", outputName: "SC_ClaimReward_Rsp",
			notify: false, kind: kindFrontend, expectError: true,
		},
		{
			name: "Rsp missing _Rsp suffix", methodName: "ClaimReward", inputName: "CS_ClaimReward_Req", outputName: "SC_ClaimReward",
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
