package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// lineHasAll 判断 content 中是否存在某一行同时包含全部给定 token。
func lineHasAll(content string, tokens ...string) bool {
	for _, line := range strings.Split(content, "\n") {
		ok := true
		for _, tk := range tokens {
			if !strings.Contains(line, tk) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// TestGenStruct 给定最小 proto，验证 gen 产出正确的 struct 文本和三张表。
func TestGenStruct(t *testing.T) {
	if _, err := exec.LookPath("protoc"); err != nil {
		t.Skip("protoc not found, skipping gen integration test")
	}

	dir := t.TempDir()
	optProto := filepath.Join(dir, "options.proto")
	svcProto := filepath.Join(dir, "test_svc.proto")

	os.WriteFile(optProto, []byte(`
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "google/protobuf/descriptor.proto";
extend google.protobuf.FieldOptions {
  bool reload   = 50101;
  bool env      = 50102;
  bool required = 50103;
}
`), 0644)

	os.WriteFile(svcProto, []byte(`
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "options.proto";
message TestSvcConfig {
  string host     = 1 [(conf.required) = true];
  int32  port     = 2 [(conf.required) = true];
  string password = 3 [(conf.env) = true, (conf.required) = true];
  bool   debug    = 4 [(conf.reload) = true];
}
`), 0644)

	pbOut := filepath.Join(dir, "config.pb.descriptor")
	protocArgs := []string{
		"--proto_path=" + dir,
		"--descriptor_set_out=" + pbOut,
		"--include_imports",
		optProto,
		svcProto,
	}
	if err := runProtoc(protocArgs); err != nil {
		t.Fatalf("protoc failed: %v", err)
	}

	outDir := t.TempDir()
	if err := generate(pbOut, outDir); err != nil {
		t.Fatalf("generate failed: %v", err)
	}

	structFile := filepath.Join(outDir, "config.go")
	got, err := os.ReadFile(structFile)
	if err != nil {
		t.Fatalf("config.go not generated: %v", err)
	}
	content := string(got)
	if !strings.Contains(content, "type TestSvcConfig struct") {
		t.Errorf("config.go missing %q\ngot:\n%s", "type TestSvcConfig struct", content)
	}
	// gofmt 会对齐 struct 字段，字段名与类型间可能有多个空格，故按 token 宽松校验：
	// 每个字段同一行需同时含字段名、Go 类型、yaml tag。
	fieldChecks := [][3]string{
		{"Host", "string", `yaml:"host"`},
		{"Port", "int32", `yaml:"port"`},
		{"Password", "string", `yaml:"password"`},
		{"Debug", "bool", `yaml:"debug"`},
	}
	for _, fc := range fieldChecks {
		if !lineHasAll(content, fc[0], fc[1], fc[2]) {
			t.Errorf("config.go missing field line with tokens %v\ngot:\n%s", fc, content)
		}
	}

	tableFile := filepath.Join(outDir, "reload_table.go")
	tableGot, err := os.ReadFile(tableFile)
	if err != nil {
		t.Fatalf("reload_table.go not generated: %v", err)
	}
	tableContent := string(tableGot)
	if !strings.Contains(tableContent, `"TestSvcConfig"`) {
		t.Errorf("reload_table.go missing %q\ngot:\n%s", `"TestSvcConfig"`, tableContent)
	}
	// gofmt 会对齐 map value，key 与 true 间可能有多个空格，故按 token 宽松校验。
	pathChecks := []string{"debug", "password", "host", "port"}
	for _, p := range pathChecks {
		if !lineHasAll(tableContent, `"`+p+`"`, "true") {
			t.Errorf("reload_table.go missing path %q with true\ngot:\n%s", p, tableContent)
		}
	}
}

// TestGenRejectsEnum 验证出现 enum 类型时 gen 报错。
func TestGenRejectsEnum(t *testing.T) {
	if _, err := exec.LookPath("protoc"); err != nil {
		t.Skip("protoc not found")
	}
	dir := t.TempDir()
	optProto := filepath.Join(dir, "options.proto")
	os.WriteFile(optProto, []byte(`
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "google/protobuf/descriptor.proto";
extend google.protobuf.FieldOptions {
  bool reload = 50101; bool env = 50102; bool required = 50103;
}
`), 0644)
	badProto := filepath.Join(dir, "bad.proto")
	os.WriteFile(badProto, []byte(`
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "options.proto";
enum Color { RED = 0; }
message BadConfig { Color c = 1; }
`), 0644)
	pbOut := filepath.Join(dir, "config.pb.descriptor")
	runProtoc([]string{"--proto_path=" + dir, "--descriptor_set_out=" + pbOut, "--include_imports", optProto, badProto})
	outDir := t.TempDir()
	err := generate(pbOut, outDir)
	if err == nil || !strings.Contains(err.Error(), "enum") {
		t.Errorf("expected enum error, got: %v", err)
	}
}

// TestGenRejectsEnvNonString 验证 env=true 但字段类型非 string 时 gen 报错。
func TestGenRejectsEnvNonString(t *testing.T) {
	if _, err := exec.LookPath("protoc"); err != nil {
		t.Skip("protoc not found")
	}
	dir := t.TempDir()
	optProto := filepath.Join(dir, "options.proto")
	os.WriteFile(optProto, []byte(`
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "google/protobuf/descriptor.proto";
extend google.protobuf.FieldOptions {
  bool reload = 50101; bool env = 50102; bool required = 50103;
}
`), 0644)
	badProto := filepath.Join(dir, "envint.proto")
	os.WriteFile(badProto, []byte(`
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "options.proto";
message EnvIntConfig { int32 port = 1 [(conf.env) = true]; }
`), 0644)
	pbOut := filepath.Join(dir, "config.pb.descriptor")
	runProtoc([]string{"--proto_path=" + dir, "--descriptor_set_out=" + pbOut, "--include_imports", optProto, badProto})
	outDir := t.TempDir()
	err := generate(pbOut, outDir)
	if err == nil || !strings.Contains(err.Error(), "env") {
		t.Errorf("expected env-type error, got: %v", err)
	}
}
