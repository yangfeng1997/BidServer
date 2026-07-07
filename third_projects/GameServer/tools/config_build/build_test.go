package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestBuild_CommonOnly common 独立渲染：只含 common 字段，不含服务私有字段。
func TestBuild_CommonOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "common.yaml"), "\nredis:\n  host: ${redis_host}\n  port: ${redis_port}\n  password: ${REDIS_PWD}\nlog:\n  level: info\n")
	writeFile(t, filepath.Join(dir, "envs", "dev.yaml"), "\nredis_host: 127.0.0.1\nredis_port: 6379\n")
	outDir := filepath.Join(t.TempDir(), "common", "conf")
	err := build(buildConfig{
		confDir:   dir,
		svc:       "common",
		env:       "dev",
		envsDir: filepath.Join(dir, "envs"),
		outDir:    outDir,
	})
	if err != nil {
		t.Fatalf("build common failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(outDir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(got)
	if strings.Contains(content, "${redis_host}") {
		t.Error("redis_host placeholder should be filled")
	}
	if !strings.Contains(content, "127.0.0.1") {
		t.Error("redis_host value missing")
	}
	if !strings.Contains(content, "${REDIS_PWD}") {
		t.Error("REDIS_PWD should be preserved")
	}
}

// TestBuild_SvcOnly 服务独立渲染：只含服务私有字段，不读 common.yaml。
func TestBuild_SvcOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gatesvr.yaml"), "\nnode_id: ${gate_node_id}\naddr: ${gate_addr}\n")
	writeFile(t, filepath.Join(dir, "envs", "dev.yaml"), "\ngate_node_id: \"1.1.1\"\ngate_addr: \"0.0.0.0:8888\"\n")
	outDir := t.TempDir()
	err := build(buildConfig{
		confDir:   dir,
		svc:       "gatesvr",
		env:       "dev",
		envsDir: filepath.Join(dir, "envs"),
		outDir:    outDir,
	})
	if err != nil {
		t.Fatalf("build gatesvr failed: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(outDir, "config.yaml"))
	content := string(got)
	if !strings.Contains(content, "1.1.1") {
		t.Error("node_id value missing")
	}
	if !strings.Contains(content, "0.0.0.0:8888") {
		t.Error("addr value missing")
	}
}

// TestBuild_MixedCaseFails 混合大小写占位符 → build 失败。
func TestBuild_MixedCaseFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gatesvr.yaml"), "redis_host: ${Redis_Host}")
	writeFile(t, filepath.Join(dir, "envs", "dev.yaml"), "")
	outDir := t.TempDir()
	err := build(buildConfig{confDir: dir, svc: "gatesvr", env: "dev",
		envsDir: filepath.Join(dir, "envs"), outDir: outDir})
	if err == nil || !strings.Contains(err.Error(), "mixed") {
		t.Errorf("expected mixed-case error, got: %v", err)
	}
}

// TestBuild_MissingValueFails 小写占位符在 values 中无对应 → build 失败。
func TestBuild_MissingValueFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gatesvr.yaml"), "host: ${missing_key}")
	writeFile(t, filepath.Join(dir, "envs", "dev.yaml"), "other_key: foo")
	outDir := t.TempDir()
	err := build(buildConfig{confDir: dir, svc: "gatesvr", env: "dev",
		envsDir: filepath.Join(dir, "envs"), outDir: outDir})
	if err == nil || !strings.Contains(err.Error(), "missing_key") {
		t.Errorf("expected missing key error, got: %v", err)
	}
}

// TestBuild_SvcFillsPlaceholder 服务 yaml 中的占位符正常填充。
func TestBuild_SvcFillsPlaceholder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gatesvr.yaml"), "\nredis:\n  host: ${gate_redis_host}\n  port: 6380\n")
	writeFile(t, filepath.Join(dir, "envs", "dev.yaml"), "\ngate_redis_host: gate-host\n")
	outDir := t.TempDir()
	err := build(buildConfig{confDir: dir, svc: "gatesvr", env: "dev",
		envsDir: filepath.Join(dir, "envs"), outDir: outDir})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(outDir, "config.yaml"))
	content := string(got)
	if !strings.Contains(content, "gate-host") {
		t.Error("gate_redis_host not filled")
	}
	if !strings.Contains(content, "6380") {
		t.Error("port missing")
	}
}

// TestBuild_SubtreeInjection 小写占位符对应整子树（${redis} → 展开成 map）。
func TestBuild_SubtreeInjection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "common.yaml"), "redis: ${redis}")
	writeFile(t, filepath.Join(dir, "envs", "dev.yaml"), "\nredis:\n  host: 127.0.0.1\n  port: 6379\n")
	outDir := t.TempDir()
	err := build(buildConfig{confDir: dir, svc: "common", env: "dev",
		envsDir: filepath.Join(dir, "envs"), outDir: outDir})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(outDir, "config.yaml"))
	content := string(got)
	if !strings.Contains(content, "host: 127.0.0.1") {
		t.Errorf("subtree injection failed, got:\n%s", content)
	}
}

// TestBuild_ResidualLowercaseFails 填值后仍有小写占位符 → build 失败。
func TestBuild_ResidualLowercaseFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gatesvr.yaml"), "host: ${unfilled}")
	writeFile(t, filepath.Join(dir, "envs", "dev.yaml"), "unfilled: ${still_missing}")
	outDir := t.TempDir()
	err := build(buildConfig{confDir: dir, svc: "gatesvr", env: "dev",
		envsDir: filepath.Join(dir, "envs"), outDir: outDir})
	if err == nil {
		t.Error("expected residual placeholder error")
	}
}

// TestBuild_EnvCrossCheck_UpperVarWithoutEnvMark yaml 字段填了大写 ${VAR} 但未在 envFields 中标记 → build 失败。
func TestBuild_EnvCrossCheck_UpperVarWithoutEnvMark(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "common.yaml"), "password: ${REDIS_PWD}")
	writeFile(t, filepath.Join(dir, "envs", "dev.yaml"), "")
	outDir := t.TempDir()
	err := build(buildConfig{
		confDir: dir, svc: "common", env: "dev",
		envsDir: filepath.Join(dir, "envs"), outDir: outDir,
		envFields: map[string]bool{},
	})
	if err == nil || !strings.Contains(err.Error(), "env") {
		t.Errorf("expected env cross-check error, got: %v", err)
	}
}

// TestBuild_EnvCrossCheck_EnvMarkButLiteral proto 标了 env 但 yaml 写的是字面量 → build 失败。
func TestBuild_EnvCrossCheck_EnvMarkButLiteral(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "common.yaml"), "password: plaintext_value")
	writeFile(t, filepath.Join(dir, "envs", "dev.yaml"), "")
	outDir := t.TempDir()
	err := build(buildConfig{
		confDir: dir, svc: "common", env: "dev",
		envsDir: filepath.Join(dir, "envs"), outDir: outDir,
		envFields: map[string]bool{"password": true},
	})
	if err == nil || !strings.Contains(err.Error(), "env") {
		t.Errorf("expected env-mark-but-literal error, got: %v", err)
	}
}

// TestBuild_SourceComments 验证输出 config.yaml 的字段携带来源注释。
func TestBuild_SourceComments(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "common.yaml"), "\nredis:\n  host: ${redis_host}\n  password: ${REDIS_PWD}\nlog:\n  level: info\n")
	writeFile(t, filepath.Join(dir, "envs", "dev.yaml"), "\nredis_host: 127.0.0.1\n")
	outDir := t.TempDir()
	if err := build(buildConfig{confDir: dir, svc: "common", env: "dev",
		envsDir: filepath.Join(dir, "envs"), outDir: outDir}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(outDir, "config.yaml"))
	content := string(got)
	if !strings.Contains(content, "envs/dev.yaml") {
		t.Errorf("expected 'from envs/dev.yaml' comment, got:\n%s", content)
	}
	if !strings.Contains(content, "env, runtime") {
		t.Errorf("expected 'env, runtime' comment for password, got:\n%s", content)
	}
}

// TestBuild_CircularPlaceholderSelf 自引用占位符 → 报错而非崩溃。
func TestBuild_CircularPlaceholderSelf(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gatesvr.yaml"), "x: ${self}")
	writeFile(t, filepath.Join(dir, "envs", "dev.yaml"), "self: ${self}")
	outDir := t.TempDir()
	err := build(buildConfig{confDir: dir, svc: "gatesvr", env: "dev",
		envsDir: filepath.Join(dir, "envs"), outDir: outDir})
	if err == nil {
		t.Fatal("expected circular reference error, got nil")
	}
}

// TestBuild_CircularPlaceholderMutual 互相引用成环 → 报错而非崩溃。
func TestBuild_CircularPlaceholderMutual(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gatesvr.yaml"), "x: ${a}")
	writeFile(t, filepath.Join(dir, "envs", "dev.yaml"), "a: ${b}\nb: ${a}")
	outDir := t.TempDir()
	err := build(buildConfig{confDir: dir, svc: "gatesvr", env: "dev",
		envsDir: filepath.Join(dir, "envs"), outDir: outDir})
	if err == nil {
		t.Fatal("expected circular reference error, got nil")
	}
}

// TestBuild_ReloadableComment reloadableFields 传入时，可热更字段挂 [reloadable] 注释。
func TestBuild_ReloadableComment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "common.yaml"), "log:\n  level: info\n")
	writeFile(t, filepath.Join(dir, "envs", "dev.yaml"), "")
	outDir := t.TempDir()
	err := build(buildConfig{confDir: dir, svc: "common", env: "dev",
		envsDir: filepath.Join(dir, "envs"), outDir: outDir,
		reloadableFields: map[string]bool{"log.level": true}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(outDir, "config.yaml"))
	if !strings.Contains(string(got), "reloadable") {
		t.Errorf("expected reloadable comment, got:\n%s", string(got))
	}
}

// TestBuild_NestedUpperPreserved values 里的值含大写 ${VAR}，填子树时大写保留并注释 env, runtime。
func TestBuild_NestedUpperPreserved(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "common.yaml"), "redis: ${redis}")
	writeFile(t, filepath.Join(dir, "envs", "dev.yaml"), "redis:\n  host: 127.0.0.1\n  password: ${REDIS_PWD}\n")
	outDir := t.TempDir()
	err := build(buildConfig{confDir: dir, svc: "common", env: "dev",
		envsDir: filepath.Join(dir, "envs"), outDir: outDir})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(outDir, "config.yaml"))
	content := string(got)
	if !strings.Contains(content, "${REDIS_PWD}") {
		t.Errorf("nested uppercase placeholder should be preserved, got:\n%s", content)
	}
}
