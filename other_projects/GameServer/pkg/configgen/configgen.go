package configgen

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var upperEnvPattern = regexp.MustCompile(`\$\{[A-Z][A-Z0-9_]*\}`)

// ── Load / LoadFiles ──

// Load 读取单个 YAML 文件，展开 ${UPPER_CASE} 环境变量后反序列化为 T
func Load[T any](path string) (T, error) {
	var zero T
	data, err := os.ReadFile(path)
	if err != nil {
		return zero, err
	}
	data, err = ExpandUpperEnv(data)
	if err != nil {
		return zero, err
	}
	return UnmarshalYAML[T](data)
}

// LoadFiles 读取多个 YAML 文件，按顺序合并后反序列化为 T
// 后面的文件覆盖前面文件中的同名字段（浅层合并）
// map[string]any 目标时零分配返回合并结果；typed struct 时通过 yaml 再反序列化
func LoadFiles[T any](paths ...string) (T, error) {
	var zero T
	if len(paths) == 0 {
		return zero, fmt.Errorf("LoadFiles: no paths provided")
	}

	merged := make(map[string]any)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return zero, fmt.Errorf("read %s: %w", path, err)
		}
		data, err = ExpandUpperEnv(data)
		if err != nil {
			return zero, fmt.Errorf("expand env %s: %w", path, err)
		}
		var m map[string]any
		dec := yaml.NewDecoder(bytes.NewReader(data))
		if err := dec.Decode(&m); err != nil {
			return zero, fmt.Errorf("parse %s: %w", path, err)
		}
		deepMerge(merged, m)
	}

	// map 目标直接返回，避免二次序列化
	if reflect.TypeOf(zero).Kind() == reflect.Map {
		return any(merged).(T), nil
	}

	// typed struct: yaml marshal → unmarshal
	mergedData, err := yaml.Marshal(merged)
	if err != nil {
		return zero, fmt.Errorf("marshal merged: %w", err)
	}
	return UnmarshalYAML[T](mergedData)
}

func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		dv, dok := dst[k].(map[string]any)
		sv, sok := v.(map[string]any)
		if dok && sok {
			deepMerge(dv, sv)
		} else {
			dst[k] = v
		}
	}
}

// UnmarshalYAML 将 YAML 数据反序列化为 T
func UnmarshalYAML[T any](data []byte) (T, error) {
	var dest T
	if reflect.TypeOf(dest).Kind() == reflect.Ptr {
		// 指针类型需要先分配内存，否则 yaml.Decode 会 nil pointer
		dest = reflect.New(reflect.TypeOf(dest).Elem()).Interface().(T)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&dest); err != nil {
		var zero T
		return zero, err
	}
	return dest, nil
}

// ── 环境变量 ──

// ExpandUpperEnv 将 ${UPPER_VAR} 替换为 OS 环境变量值，找不到则报错
func ExpandUpperEnv(data []byte) ([]byte, error) {
	text := string(data)
	missing := make([]string, 0)
	text = upperEnvPattern.ReplaceAllStringFunc(text, func(token string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(token, "${"), "}")
		val, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return token
		}
		return val
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("env vars not injected: %v", missing)
	}
	return []byte(text), nil
}

// ── 占位符分类（供 config_build 使用）──

// ClassifyPlaceholder 分类占位符：返回 lower=true / upper=true
// 同时包含大小写字母为 mixed，需要报错
func ClassifyPlaceholder(token string) (lower, upper bool) {
	hasLower, hasUpper := false, false
	for _, r := range token {
		if r >= 'a' && r <= 'z' {
			hasLower = true
		}
		if r >= 'A' && r <= 'Z' {
			hasUpper = true
		}
	}
	return hasLower, hasUpper
}
