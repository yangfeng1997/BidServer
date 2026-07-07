package configgen

import (
	"os"
	"path/filepath"
	"testing"

	"project/conf/schema/gen"
)

// ── 完整管线：模板 → config_build → configgen.Load → 类型化 struct ──

func TestFullPipeline_LoadBakedConfig(t *testing.T) {
	tmpDir := t.TempDir()

	values := map[string]string{
		"redis_host": "10.0.0.1",
		"redis_port": "6379",
	}
	tmpl := `redis:
  host: "${redis_host}"
  port: ${redis_port}
`
	tmplPath := filepath.Join(tmpDir, "tmpl.yaml")
	os.WriteFile(tmplPath, []byte(tmpl), 0o644)

	baked := bakeTest(tmplPath, values)
	bakedPath := filepath.Join(tmpDir, "baked.yaml")
	os.WriteFile(bakedPath, []byte(baked), 0o644)

	m, err := Load[map[string]any](bakedPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	redis := m["redis"].(map[string]any)
	if redis["host"] != "10.0.0.1" {
		t.Errorf("redis.host=%v, want 10.0.0.1", redis["host"])
	}
	if redis["port"] != 6379 {
		t.Errorf("redis.port=%v, want 6379", redis["port"])
	}
}

// ── CommonConfig 类型化加载 ──

func TestFullPipeline_CommonConfig(t *testing.T) {
	tmpDir := t.TempDir()

	yamlContent := `node:
  world_id: 1
  server_type: 1
  server_index: 0
etcd:
  endpoints:
    - "127.0.0.1:2379"
redis:
  host: "127.0.0.1"
  port: 6379
`
	yamlPath := filepath.Join(tmpDir, "common.yaml")
	os.WriteFile(yamlPath, []byte(yamlContent), 0o644)

	cfg, err := Load[*gen.CommonConfig](yamlPath)
	if err != nil {
		t.Fatalf("Load CommonConfig: %v", err)
	}

	if cfg.Node == nil || cfg.Node.WorldId != 1 {
		t.Errorf("node.world_id=%d", cfg.Node.WorldId)
	}
	if cfg.Etcd == nil || len(cfg.Etcd.Endpoints) != 1 {
		t.Errorf("etcd.endpoints=%v", cfg.Etcd.Endpoints)
	}
	if cfg.Redis == nil || cfg.Redis.Host != "127.0.0.1" {
		t.Errorf("redis.host=%s", cfg.Redis.Host)
	}
}

// ── GatesvrConfig 类型化加载（只有服务专有字段）──

func TestFullPipeline_TypedGateConfig(t *testing.T) {
	tmpDir := t.TempDir()

	yamlContent := `gate:
  listen_tcp: "0.0.0.0:7001"
  listen_ws: "0.0.0.0:7002"
  drain_timeout_sec: 5
  max_conn: 10000
  log_level: info
  heartbeat_sec: 30
`
	yamlPath := filepath.Join(tmpDir, "gatesvr.yaml")
	os.WriteFile(yamlPath, []byte(yamlContent), 0o644)

	cfg, err := Load[*gen.GatesvrConfig](yamlPath)
	if err != nil {
		t.Fatalf("Load typed error: %v", err)
	}

	if cfg.Gate == nil {
		t.Fatal("Gate is nil")
	}
	if cfg.Gate.ListenTcp != "0.0.0.0:7001" {
		t.Errorf("gate.listen_tcp=%s", cfg.Gate.ListenTcp)
	}
	if cfg.Gate.MaxConn != 10000 {
		t.Errorf("gate.max_conn=%d", cfg.Gate.MaxConn)
	}
	if cfg.Gate.LogLevel != "info" {
		t.Errorf("gate.log_level=%s", cfg.Gate.LogLevel)
	}
	if cfg.Gate.HeartbeatSec != 30 {
		t.Errorf("gate.heartbeat_sec=%d", cfg.Gate.HeartbeatSec)
	}
}

// ── 所有 6 个服务类型化 Load ──

func TestFullPipeline_AllServiceTypes(t *testing.T) {
	tmpDir := t.TempDir()

	services := []struct {
		name   string
		yaml   string
		loadFn func(path string) error
	}{
		{
			name:   "gatesvr",
			yaml:   `gate: {listen_tcp: "0.0.0.0:7001", listen_ws: "0.0.0.0:7002", drain_timeout_sec: 5, max_conn: 10000, log_level: info, heartbeat_sec: 30}`,
			loadFn: func(p string) error { _, err := Load[*gen.GatesvrConfig](p); return err },
		},
		{
			name:   "lobbysvr",
			yaml:   `lobby: {log_level: info, heartbeat_sec: 30, max_player: 5000}`,
			loadFn: func(p string) error { _, err := Load[*gen.LobbysvrConfig](p); return err },
		},
		{
			name:   "roomsvr",
			yaml:   `room: {log_level: info, max_room: 2000}`,
			loadFn: func(p string) error { _, err := Load[*gen.RoomsvrConfig](p); return err },
		},
		{
			name:   "matchsvr",
			yaml:   `match: {log_level: info, match_tick_ms: 100}`,
			loadFn: func(p string) error { _, err := Load[*gen.MatchsvrConfig](p); return err },
		},
		{
			name:   "onlinesvr",
			yaml:   `online: {log_level: info, max_online: 100000}`,
			loadFn: func(p string) error { _, err := Load[*gen.OnlinesvrConfig](p); return err },
		},
		{
			name:   "routeragent",
			yaml:   `router_agent: {log_level: info, heartbeat_sec: 30}`,
			loadFn: func(p string) error { _, err := Load[*gen.RouteragentConfig](p); return err },
		},
	}

	for _, svc := range services {
		t.Run(svc.name, func(t *testing.T) {
			yamlPath := filepath.Join(tmpDir, svc.name+".yaml")
			os.WriteFile(yamlPath, []byte(svc.yaml), 0o644)
			if err := svc.loadFn(yamlPath); err != nil {
				t.Fatalf("Load %s: %v", svc.name, err)
			}
		})
	}
}

// ── KnownFields 严格模式：多余字段报错 ──

func TestFullPipeline_KnownFieldsRejectsExtra(t *testing.T) {
	tmpDir := t.TempDir()

	yamlWithExtra := `gate: {listen_tcp: "0.0.0.0:7001", listen_ws: "0.0.0.0:7002", max_conn: 10000, log_level: info, heartbeat_sec: 30, unknown_field: "bad"}`
	yamlPath := filepath.Join(tmpDir, "extra.yaml")
	os.WriteFile(yamlPath, []byte(yamlWithExtra), 0o644)

	_, err := Load[*gen.GatesvrConfig](yamlPath)
	if err == nil {
		t.Fatal("expected error for unknown field in strict mode")
	}
	t.Logf("KnownFields rejected extra field: %v", err)
}

// ── 热更保护：DiffFields 与真实 ReloadableFields 表 ──


// ── 递归全深度 DiffFields ──

// ── LoadFiles 非重叠键合并 ──

func TestFullPipeline_LoadFilesMerge(t *testing.T) {
	tmpDir := t.TempDir()

	basePath := filepath.Join(tmpDir, "base.yaml")
	overridePath := filepath.Join(tmpDir, "override.yaml")

	os.WriteFile(basePath, []byte(`gate: {listen_tcp: "0.0.0.0:7001", listen_ws: "0.0.0.0:7002", max_conn: 10000, log_level: info, heartbeat_sec: 30, drain_timeout_sec: 5}`), 0o644)
	os.WriteFile(overridePath, []byte(`log: {main: {level: info, format: console, dir: "./logs", basename: gatesvr}}`), 0o644)

	cfg, err := LoadFiles[*gen.GatesvrConfig](basePath, overridePath)
	if err != nil {
		t.Fatalf("LoadFiles error: %v", err)
	}
	if cfg.Gate.MaxConn != 10000 {
		t.Errorf("gate.max_conn=%d, want 10000", cfg.Gate.MaxConn)
	}
	if cfg.Log == nil || cfg.Log.Main == nil || cfg.Log.Main.Basename != "gatesvr" {
		t.Errorf("log.main should be from override")
	}
}

// LoadFiles deep merge：嵌套 map 递归合并
func TestFullPipeline_LoadFilesDeepMerge(t *testing.T) {
	tmpDir := t.TempDir()

	basePath := filepath.Join(tmpDir, "base_dm.yaml")
	overridePath := filepath.Join(tmpDir, "override_dm.yaml")

	os.WriteFile(basePath, []byte(`gate: {listen_tcp: "0.0.0.0:7001", listen_ws: "0.0.0.0:7002", max_conn: 10000, log_level: info, heartbeat_sec: 30, drain_timeout_sec: 5}
`), 0o644)
	os.WriteFile(overridePath, []byte(`gate: {listen_tcp: "0.0.0.0:8001", max_conn: 99999, log_level: warn}
`), 0o644)

	cfg, err := LoadFiles[*gen.GatesvrConfig](basePath, overridePath)
	if err != nil {
		t.Fatalf("LoadFiles error: %v", err)
	}

	if cfg.Gate.ListenTcp != "0.0.0.0:8001" {
		t.Errorf("gate.listen_tcp=%s, want 0.0.0.0:8001", cfg.Gate.ListenTcp)
	}
	if cfg.Gate.MaxConn != 99999 {
		t.Errorf("gate.max_conn=%d, want 99999", cfg.Gate.MaxConn)
	}
	// deep merge preserves unmentioned fields from base
	if cfg.Gate.ListenWs != "0.0.0.0:7002" {
		t.Errorf("gate.listen_ws=%s, should be preserved from base", cfg.Gate.ListenWs)
	}
	if cfg.Gate.HeartbeatSec != 30 {
		t.Errorf("gate.heartbeat_sec=%d, should be preserved from base", cfg.Gate.HeartbeatSec)
	}
}

// ── 环境变量注入 ──

func TestFullPipeline_EnvVarInjection(t *testing.T) {
	tmpDir := t.TempDir()

	os.Setenv("MY_REDIS_HOST", "redis.prod.internal")
	os.Setenv("MY_REDIS_PORT", "6380")
	defer os.Unsetenv("MY_REDIS_HOST")
	defer os.Unsetenv("MY_REDIS_PORT")

	yamlWithEnv := `node: {world_id: 1, server_type: 1, server_index: 0}
etcd: {endpoints: ["127.0.0.1:2379"]}
redis: {host: ${MY_REDIS_HOST}, port: ${MY_REDIS_PORT}}
`
	yamlPath := filepath.Join(tmpDir, "env.yaml")
	os.WriteFile(yamlPath, []byte(yamlWithEnv), 0o644)

	cfg, err := Load[*gen.CommonConfig](yamlPath)
	if err != nil {
		t.Fatalf("Load with env: %v", err)
	}
	if cfg.Redis.Host != "redis.prod.internal" {
		t.Errorf("redis.host=%s, want redis.prod.internal", cfg.Redis.Host)
	}
	if cfg.Redis.Port != 6380 {
		t.Errorf("redis.port=%d, want 6380", cfg.Redis.Port)
	}
}

// ── Required 字段缺失检测 ──

// ── LoadFiles 作为 map ──

func TestFullPipeline_LoadFilesAsMap(t *testing.T) {
	tmpDir := t.TempDir()

	basePath := filepath.Join(tmpDir, "base_map.yaml")
	overridePath := filepath.Join(tmpDir, "override_map.yaml")

	os.WriteFile(basePath, []byte(`node: {world_id: 1, server_type: 2, server_index: 0}
etcd: {endpoints: ["base-etcd:2379"]}
`), 0o644)
	os.WriteFile(overridePath, []byte(`etcd: {endpoints: ["override-etcd:2379"]}
redis: {host: "added-redis", port: 6380}
`), 0o644)

	m, err := LoadFiles[map[string]any](basePath, overridePath)
	if err != nil {
		t.Fatalf("LoadFiles error: %v", err)
	}

	etcd := m["etcd"].(map[string]any)
	if etcd["endpoints"].([]any)[0] != "override-etcd:2379" {
		t.Errorf("etcd.endpoints not overridden")
	}
	node := m["node"].(map[string]any)
	if node["world_id"].(int) != 1 {
		t.Errorf("node.world_id should be preserved")
	}
	redis := m["redis"].(map[string]any)
	if redis["host"] != "added-redis" {
		t.Errorf("redis.host=%v, want added-redis", redis["host"])
	}
}

// ── 辅助函数 ──

func bakeTest(tmplPath string, values map[string]string) string {
	data, _ := os.ReadFile(tmplPath)
	text := string(data)
	for k, v := range values {
		text = replaceAllLiteral(text, "${"+k+"}", v)
	}
	return text
}

func replaceAllLiteral(s, old, new string) string {
	result := ""
	for {
		idx := indexOf(s, old)
		if idx < 0 {
			result += s
			break
		}
		result += s[:idx] + new
		s = s[idx+len(old):]
	}
	return result
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

