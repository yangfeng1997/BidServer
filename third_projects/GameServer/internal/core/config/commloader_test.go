package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSplitFiles(t *testing.T) {
	dir := t.TempDir()
	commonDir := filepath.Join(dir, "run", "common", "conf")
	mustMkdirAll(t, commonDir)
	writeFile(t, commonDir, "common.yaml", "node:\n  world_id: 1\n  server_type: 1\n  server_index: 0\n")

	svcDir := filepath.Join(dir, "run", "gatesvr", "conf")
	mustMkdirAll(t, svcDir)
	writeFile(t, svcDir, "svc.yaml", "gate:\n  listen_tcp: \"0.0.0.0:7001\"\n  listen_ws: \"0.0.0.0:7002\"\n  max_conn: 10000\n  log_level: info\n  heartbeat_sec: 30\n")
	writeFile(t, svcDir, "svc_log.yaml", "log:\n  main:\n    level: info\n    format: console\n    dir: /tmp/log\n    basename: gatesvr\n")

	commonFiles, svcFiles := SplitFiles([]string{
		filepath.Join(commonDir, "common.yaml"),
		filepath.Join(svcDir, "svc.yaml"),
		filepath.Join(svcDir, "svc_log.yaml"),
	})

	if len(commonFiles) != 1 {
		t.Fatalf("expected 1 common file, got %d: %v", len(commonFiles), commonFiles)
	}
	if len(svcFiles) != 2 {
		t.Fatalf("expected 2 svc files, got %d: %v", len(svcFiles), svcFiles)
	}
	if !contains(commonFiles[0], "/common/") {
		t.Fatalf("common file path must contain /common/, got %s", commonFiles[0])
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contains(path, substr string) bool {
	for i := 0; i <= len(path)-len(substr); i++ {
		if path[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
