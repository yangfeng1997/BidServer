package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFlatten(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want map[string]string
	}{
		{
			name: "flat",
			in:   map[string]any{"host": "localhost", "port": 8080},
			want: map[string]string{"host": "localhost", "port": "8080"},
		},
		{
			name: "nested",
			in: map[string]any{
				"redis": map[string]any{"host": "127.0.0.1", "port": 6379},
				"etcd":  map[string]any{"endpoint": "127.0.0.1:2379"},
			},
			want: map[string]string{
				"redis.host":     "127.0.0.1",
				"redis.port":     "6379",
				"etcd.endpoint":  "127.0.0.1:2379",
			},
		},
		{
			name: "deeply nested",
			in: map[string]any{
				"a": map[string]any{
					"b": map[string]any{
						"c": "deep",
					},
				},
			},
			want: map[string]string{"a.b.c": "deep"},
		},
		{
			name: "empty",
			in:   map[string]any{},
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := make(map[string]string)
			flatten(tt.in, "", out)
			if len(out) != len(tt.want) {
				t.Errorf("len=%d, want %d; got=%v", len(out), len(tt.want), out)
				return
			}
			for k, v := range tt.want {
				if out[k] != v {
					t.Errorf("%s: got %q, want %q", k, out[k], v)
				}
			}
		})
	}
}

func TestBakeYAML(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("simple replacement", func(t *testing.T) {
		tmpl := filepath.Join(tmpDir, "tmpl.yaml")
		os.WriteFile(tmpl, []byte("host: ${db_host}\nport: ${db_port}\n"), 0o644)

		values := map[string]string{
			"db_host": "10.0.0.1",
			"db_port": "5432",
		}
		out := filepath.Join(tmpDir, "out.yaml")

		if err := bakeYAML(tmpl, out, values); err != nil {
			t.Fatalf("bakeYAML failed: %v", err)
		}
		got, _ := os.ReadFile(out)
		if string(got) != "host: 10.0.0.1\nport: 5432\n" {
			t.Errorf("got %q", string(got))
		}
	})

	t.Run("missing placeholder errors", func(t *testing.T) {
		tmpl := filepath.Join(tmpDir, "tmpl_missing.yaml")
		os.WriteFile(tmpl, []byte("host: ${missing_key}\n"), 0o644)

		out := filepath.Join(tmpDir, "out_missing.yaml")
		err := bakeYAML(tmpl, out, map[string]string{})
		if err == nil {
			t.Fatal("expected error for missing placeholder")
		}
	})

	t.Run("mixed case placeholder errors", func(t *testing.T) {
		tmpl := filepath.Join(tmpDir, "tmpl_mixed.yaml")
		os.WriteFile(tmpl, []byte("host: ${Mixed_Host}\n"), 0o644)

		out := filepath.Join(tmpDir, "out_mixed.yaml")
		err := bakeYAML(tmpl, out, map[string]string{"Mixed_Host": "x"})
		if err == nil {
			t.Fatal("expected error for mixed-case placeholder")
		}
	})

	t.Run("upper case preserved", func(t *testing.T) {
		tmpl := filepath.Join(tmpDir, "tmpl_upper.yaml")
		os.WriteFile(tmpl, []byte("pass: ${REDIS_PWD}\n"), 0o644)

		values := map[string]string{}
		out := filepath.Join(tmpDir, "out_upper.yaml")

		if err := bakeYAML(tmpl, out, values); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, _ := os.ReadFile(out)
		if string(got) != "pass: ${REDIS_PWD}\n" {
			t.Errorf("upper case should be preserved, got %q", string(got))
		}
	})

	t.Run("non-string values converted", func(t *testing.T) {
		tmpl := filepath.Join(tmpDir, "tmpl_int.yaml")
		os.WriteFile(tmpl, []byte("port: ${int_port}\n"), 0o644)

		values := map[string]string{"int_port": "6379"}
		out := filepath.Join(tmpDir, "out_int.yaml")

		if err := bakeYAML(tmpl, out, values); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, _ := os.ReadFile(out)
		if string(got) != "port: 6379\n" {
			t.Errorf("got %q", string(got))
		}
	})
}

func TestFindMixedPlaceholders(t *testing.T) {
	tests := []struct {
		text string
		want int // expected count of mixed placeholders
	}{
		{"${lower_case} ${ANOTHER_LOWER}", 0},     // all lower → not mixed
		{"${UPPER_CASE} ${MORE_UPPER}", 0},         // all upper → not mixed
		{"${Mixed_Case}", 1},                        // true mixed
		{"${lower} ${UPPER} ${MixedHere}", 1},       // one mixed
		{"${camelCase} ${snake_case} ${ALLCAPS}", 1}, // one mixed
		{"no placeholders", 0},
	}

	for _, tt := range tests {
		got := findMixedPlaceholders(tt.text)
		if len(got) != tt.want {
			t.Errorf("findMixedPlaceholders(%q)=%v (len=%d), want len=%d", tt.text, got, len(got), tt.want)
		}
	}
}
