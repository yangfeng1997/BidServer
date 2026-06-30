package config_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	conf "project/conf/schema/gen"
	cfgpkg "project/src/common/config"
)

const commonYAML = `
redis:
  host: "127.0.0.1"
  port: 6379
  timeout: 2
  password: "secret"
etcd:
  endpoints:
    - "localhost:2379"
nats:
  urls:
    - "nats://localhost:4222"
mongo:
  uri: "mongodb://localhost:27017"
  database: game
log:
  level: info
  dir: ./logs
version: "1.0.0"
region: default
excel_path: ./data/excel
`

const gateSvrYAML = `
app_startup:
  pre_application_list: []
  pre_application_path: ./bin
  pre_mine_application_list: []
node_id: "1.1.1"
server_type_name: gatesvr
addr: "0.0.0.0:8888"
heartbeat_sec: 30
shutdown_timeout_sec: 10
`

func writeYAML(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadCommon_Basic(t *testing.T) {
	path := writeYAML(t, t.TempDir(), commonYAML)
	commonCfg, err := cfgpkg.LoadCommon(path)
	if err != nil {
		t.Fatalf("LoadCommon failed: %v", err)
	}
	if commonCfg.Redis.Host != "127.0.0.1" {
		t.Errorf("redis.host mismatch: %q", commonCfg.Redis.Host)
	}
}

func TestLoad_GateSvr_Basic(t *testing.T) {
	path := writeYAML(t, t.TempDir(), gateSvrYAML)
	loader := cfgpkg.NewLoader[conf.GateSvr](path)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load GateSvr failed: %v", err)
	}
	cfg := loader.Current()
	if cfg.NodeId != "1.1.1" {
		t.Errorf("node_id mismatch: %q", cfg.NodeId)
	}
}

func TestLoad_StrictModeRejectsUnknownKey(t *testing.T) {
	yaml := gateSvrYAML + "\nunknown_key: bad_value\n"
	path := writeYAML(t, t.TempDir(), yaml)
	loader := cfgpkg.NewLoader[conf.GateSvr](path)
	if err := loader.Load(); err == nil {
		t.Fatal("expected strict-mode error for unknown field")
	}
}

func TestLoad_RequiredFieldMissing(t *testing.T) {
	yaml := `
app_startup:
  pre_application_path: ./bin
server_type_name: gatesvr
addr: "0.0.0.0:8888"
`
	path := writeYAML(t, t.TempDir(), yaml)
	loader := cfgpkg.NewLoader[conf.GateSvr](path)
	if err := loader.Load(); err == nil {
		t.Fatal("expected required field error for missing node_id")
	}
}

func TestLoad_EnvInjectionMissing(t *testing.T) {
	os.Unsetenv("MONGO_URI_TEST")
	yaml := `
redis:
  host: "127.0.0.1"
  port: 6379
  password: "secret"
etcd:
  endpoints: ["localhost:2379"]
nats:
  urls: ["nats://localhost:4222"]
mongo:
  uri: "${MONGO_URI_TEST}"
  database: game
`
	path := writeYAML(t, t.TempDir(), yaml)
	_, err := cfgpkg.LoadCommon(path)
	if err == nil {
		t.Fatal("expected env injection error for missing MONGO_URI_TEST")
	}
}

func TestLoad_EnvInjection_FillsValue(t *testing.T) {
	os.Setenv("MONGO_URI_TEST", "mongodb://testhost:27017")
	t.Cleanup(func() { os.Unsetenv("MONGO_URI_TEST") })
	yaml := `
redis:
  host: "127.0.0.1"
  port: 6379
  password: "secret"
etcd:
  endpoints: ["localhost:2379"]
nats:
  urls: ["nats://localhost:4222"]
mongo:
  uri: "${MONGO_URI_TEST}"
  database: game
`
	path := writeYAML(t, t.TempDir(), yaml)
	commonCfg, err := cfgpkg.LoadCommon(path)
	if err != nil {
		t.Fatalf("LoadCommon failed: %v", err)
	}
	if commonCfg.Mongo.Uri != "mongodb://testhost:27017" {
		t.Errorf("mongo.uri not injected: %q", commonCfg.Mongo.Uri)
	}
}

func TestReload_StaticFieldRejected(t *testing.T) {
	dir := t.TempDir()
	path := writeYAML(t, dir, gateSvrYAML)
	loader := cfgpkg.NewLoader[conf.GateSvr](path)
	if err := loader.Load(); err != nil {
		t.Fatal(err)
	}

	newYAML := `
app_startup:
  pre_application_list: []
  pre_application_path: ./bin
  pre_mine_application_list: []
node_id: "9.9.9"
server_type_name: gatesvr
addr: "0.0.0.0:9999"
heartbeat_sec: 30
shutdown_timeout_sec: 10
`
	os.WriteFile(path, []byte(newYAML), 0644)
	if err := loader.Reload(); err == nil {
		t.Fatal("expected reload rejection for static field change (node_id)")
	}
	if loader.Current().NodeId != "1.1.1" {
		t.Error("snapshot should not change after rejected reload")
	}
}

func TestReload_ReloadableFieldAccepted(t *testing.T) {
	dir := t.TempDir()
	path := writeYAML(t, dir, gateSvrYAML)
	loader := cfgpkg.NewLoader[conf.GateSvr](path)
	if err := loader.Load(); err != nil {
		t.Fatal(err)
	}

	newYAML := `
app_startup:
  pre_application_list: []
  pre_application_path: ./bin
  pre_mine_application_list: []
node_id: "1.1.1"
server_type_name: gatesvr
addr: "0.0.0.0:8888"
heartbeat_sec: 60
shutdown_timeout_sec: 10
`
	os.WriteFile(path, []byte(newYAML), 0644)
	if err := loader.Reload(); err != nil {
		t.Fatalf("reload of reloadable field should succeed: %v", err)
	}
	if loader.Current().HeartbeatSec != 60 {
		t.Errorf("heartbeat_sec should be 60, got %d", loader.Current().HeartbeatSec)
	}
}

func TestConcurrentReads(t *testing.T) {
	path := writeYAML(t, t.TempDir(), gateSvrYAML)
	loader := cfgpkg.NewLoader[conf.GateSvr](path)
	_ = loader.Load()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = loader.Current()
			}
		}()
	}
	wg.Wait()
}
