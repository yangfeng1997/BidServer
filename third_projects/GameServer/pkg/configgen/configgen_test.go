package configgen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandUpperEnv(t *testing.T) {
	os.Setenv("TEST_VAR", "hello_world")
	defer os.Unsetenv("TEST_VAR")

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "replace env var",
			input: "host: ${TEST_VAR}\nport: 6379\n",
			want:  "host: hello_world\nport: 6379\n",
		},
		{
			name:  "no placeholders",
			input: "key: value\nother: 123\n",
			want:  "key: value\nother: 123\n",
		},
		{
			name:    "missing env var",
			input:   "pwd: ${MISSING_VAR}\n",
			wantErr: true,
		},
		{
			name:  "multiple placeholders",
			input: "a: ${TEST_VAR}\nb: ${TEST_VAR}\n",
			want:  "a: hello_world\nb: hello_world\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandUpperEnv([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("got %q, want %q", string(got), tt.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "test.yaml")
	os.WriteFile(yamlPath, []byte("key: value\nnum: 42\n"), 0o644)

	m, err := Load[map[string]any](yamlPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if m["key"] != "value" {
		t.Errorf("key=%v, want value", m["key"])
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load[map[string]any]("/nonexistent/file.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadWithEnv(t *testing.T) {
	os.Setenv("ENV_TEST", "env_value")
	defer os.Unsetenv("ENV_TEST")

	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "test_env.yaml")
	os.WriteFile(yamlPath, []byte("host: ${ENV_TEST}\n"), 0o644)

	m, err := Load[map[string]any](yamlPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if m["host"] != "env_value" {
		t.Errorf("host=%v, want env_value", m["host"])
	}
}

func TestLoadTypedStruct(t *testing.T) {
	type serverConfig struct {
		Host string `yaml:"host"`
		Port int32  `yaml:"port"`
	}

	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "typed.yaml")
	os.WriteFile(yamlPath, []byte("host: localhost\nport: 8080\n"), 0o644)

	cfg, err := Load[*serverConfig](yamlPath)
	if err != nil {
		t.Fatalf("Load typed error: %v", err)
	}
	if cfg.Host != "localhost" {
		t.Errorf("host=%q, want localhost", cfg.Host)
	}
	if cfg.Port != 8080 {
		t.Errorf("port=%d, want 8080", cfg.Port)
	}
}

func TestLoadFiles(t *testing.T) {
	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "base.yaml")
	overridePath := filepath.Join(tmpDir, "override.yaml")

	os.WriteFile(basePath, []byte("host: localhost\nport: 8080\n"), 0o644)
	os.WriteFile(overridePath, []byte("host: override-host\n"), 0o644)

	m, err := LoadFiles[map[string]any](basePath, overridePath)
	if err != nil {
		t.Fatalf("LoadFiles error: %v", err)
	}
	if m["host"] != "override-host" {
		t.Errorf("host=%v, want override-host", m["host"])
	}
	if m["port"] != 8080 {
		t.Errorf("port=%v, want 8080", m["port"])
	}
}

func TestLoadFilesEmpty(t *testing.T) {
	_, err := LoadFiles[map[string]any]()
	if err == nil {
		t.Error("expected error for empty paths")
	}
}


func TestClassifyPlaceholder(t *testing.T) {
	tests := []struct {
		token      string
		wantLower  bool
		wantUpper  bool
	}{
		{"lower_case", true, false},
		{"UPPER_CASE", false, true},
		{"Mixed_Case", true, true},
		{"no_upper", true, false},
		{"NO_LOWER", false, true},
		{"123", false, false},
		{"snake_case_123", true, false},
		{"CAPS_123", false, true},
	}

	for _, tt := range tests {
		gotLower, gotUpper := ClassifyPlaceholder(tt.token)
		if gotLower != tt.wantLower || gotUpper != tt.wantUpper {
			t.Errorf("ClassifyPlaceholder(%q) = (lower=%v, upper=%v), want (lower=%v, upper=%v)",
				tt.token, gotLower, gotUpper, tt.wantLower, tt.wantUpper)
		}
	}
}
