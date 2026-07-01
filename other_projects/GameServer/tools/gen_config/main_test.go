package main

import (
	"testing"

	"google.golang.org/protobuf/types/descriptorpb"
)

func TestProtoTypeToGo(t *testing.T) {
	tests := []struct {
		pt       descriptorpb.FieldDescriptorProto_Type
		repeated bool
		typeName string
		want     string
	}{
		{descriptorpb.FieldDescriptorProto_TYPE_BOOL, false, "", "bool"},
		{descriptorpb.FieldDescriptorProto_TYPE_INT32, false, "", "int32"},
		{descriptorpb.FieldDescriptorProto_TYPE_INT64, false, "", "int64"},
		{descriptorpb.FieldDescriptorProto_TYPE_UINT32, false, "", "uint32"},
		{descriptorpb.FieldDescriptorProto_TYPE_UINT64, false, "", "uint64"},
		{descriptorpb.FieldDescriptorProto_TYPE_FLOAT, false, "", "float32"},
		{descriptorpb.FieldDescriptorProto_TYPE_DOUBLE, false, "", "float64"},
		{descriptorpb.FieldDescriptorProto_TYPE_STRING, false, "", "string"},
		{descriptorpb.FieldDescriptorProto_TYPE_BYTES, false, "", "[]byte"},
		{descriptorpb.FieldDescriptorProto_TYPE_STRING, true, "", "[]string"},
		{descriptorpb.FieldDescriptorProto_TYPE_UINT32, true, "", "[]uint32"},
		{descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, false, ".conf.schema.NodeConfig", "*NodeConfig"},
		{descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, true, ".conf.schema.GateConfig", "[]*GateConfig"},
	}
	for _, tt := range tests {
		got, _ := protoTypeToGo(tt.pt, tt.repeated, tt.typeName)
		if got != tt.want {
			t.Errorf("protoTypeToGo(%v, %v, %q)=%q, want %q", tt.pt, tt.repeated, tt.typeName, got, tt.want)
		}
	}
}

func TestSnakeToPascal(t *testing.T) {
	tests := map[string]string{
		"world_id":      "WorldId",
		"server_type":   "ServerType",
		"server_index":  "ServerIndex",
		"listen_tcp":    "ListenTcp",
		"listen_ws":     "ListenWs",
		"log_level":     "LogLevel",
		"heartbeat_sec": "HeartbeatSec",
		"max_conn":      "MaxConn",
		"router_agent":  "RouterAgent",
	}
	for input, expected := range tests {
		if got := snakeToPascal(input); got != expected {
			t.Errorf("snakeToPascal(%q)=%q, want %q", input, got, expected)
		}
	}
}

func TestParseCustomOptions(t *testing.T) {
	// Test with nil/empty bytes
	reload, required, env, enumValues := parseCustomOptions(nil)
	if reload || required || env || enumValues != "" {
		t.Error("nil bytes should return all defaults")
	}

	reload, required, env, enumValues = parseCustomOptions([]byte{})
	if reload || required || env || enumValues != "" {
		t.Error("empty bytes should return all defaults")
	}
}
