// Package config 提供运行时配置加载、环境变量注入、热更与并发安全读取。
package config

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	conf "project/conf/schema/gen"
)

var upperPlaceholderRe = regexp.MustCompile(`^\$\{([A-Z][A-Z0-9_]*)\}$`)

// LoadCommon 加载 run/common/conf/config.yaml 到 Common。
// 框架层统一调用，服务无需感知路径。
func LoadCommon(path string) (*conf.Common, error) {
	l := &Loader[conf.Common]{path: path}
	return l.loadAndValidate()
}

// Loader 加载、校验、热更单个服务配置，提供并发安全快照读取。
// T 是 gen_config 生成的服务 Config struct（如 GateSvr）。
type Loader[T any] struct {
	path    string
	current atomic.Pointer[T]
}

// NewLoader 创建 Loader，path 为 run/{svc}/conf/config.yaml。
func NewLoader[T any](path string) *Loader[T] {
	return &Loader[T]{path: path}
}

// Load 首次加载，失败返回 error（调用方应 panic 或 os.Exit）。
func (l *Loader[T]) Load() error {
	cfg, err := l.loadAndValidate()
	if err != nil {
		return err
	}
	l.current.Store(cfg)
	return nil
}

// MustLoad 加载失败时 panic。
func (l *Loader[T]) MustLoad() {
	if err := l.Load(); err != nil {
		panic("config.MustLoad: " + err.Error())
	}
}

// Current 返回当前配置快照（atomic，并发安全）。
func (l *Loader[T]) Current() *T {
	return l.current.Load()
}

// Reload 重加载；静态字段变化则拒绝，保留旧快照。
func (l *Loader[T]) Reload() error {
	newCfg, err := l.loadAndValidate()
	if err != nil {
		return fmt.Errorf("reload parse failed: %w", err)
	}
	if old := l.current.Load(); old != nil {
		if err := checkStaticFieldsUnchanged(old, newCfg); err != nil {
			return fmt.Errorf("reload rejected (static field changed): %w", err)
		}
	}
	l.current.Store(newCfg)
	return nil
}

// Watch 启动热更监听，返回 stop 函数。
// 生产靠 SIGHUP（kill -HUP <pid>）；开发备选 mtime 轮询（pollInterval>0 时启用）。
func (l *Loader[T]) Watch(pollInterval time.Duration) (stop func()) {
	done := make(chan struct{})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-sigCh:
				if err := l.Reload(); err != nil {
					fmt.Fprintf(os.Stderr, "config reload (SIGHUP) error: %v\n", err)
				} else {
					fmt.Fprintln(os.Stderr, "config reloaded via SIGHUP")
				}
			}
		}
	}()

	if pollInterval > 0 {
		var lastMtime time.Time
		if info, err := os.Stat(l.path); err == nil {
			lastMtime = info.ModTime()
		}
		ticker := time.NewTicker(pollInterval)
		go func() {
			for {
				select {
				case <-done:
					ticker.Stop()
					return
				case <-ticker.C:
					info, err := os.Stat(l.path)
					if err != nil {
						continue
					}
					if info.ModTime().After(lastMtime) {
						lastMtime = info.ModTime()
						if err := l.Reload(); err != nil {
							fmt.Fprintf(os.Stderr, "config reload (mtime) error: %v\n", err)
						}
					}
				}
			}
		}()
	}

	return func() {
		signal.Stop(sigCh)
		close(done)
	}
}

// loadAndValidate 完整加载流程：读文件 → 节点级大写注入 → 严格 Decode → Validate。
func (l *Loader[T]) loadAndValidate() (*T, error) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", l.path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("yaml parse %q: %w", l.path, err)
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		if err := injectEnv(doc.Content[0]); err != nil {
			return nil, err
		}
	}

	// re-marshal → strict decode（KnownFields 不支持直接从 Node decode，需要绕一圈）
	var cfg T
	buf := new(bytes.Buffer)
	enc := yaml.NewEncoder(buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("yaml re-encode: %w", err)
	}
	dec := yaml.NewDecoder(buf)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("yaml decode %q: %w", l.path, err)
	}

	msgName := reflect.TypeOf(cfg).Name()
	if err := validateRequired(&cfg, msgName); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// injectEnv 节点级：${UPPER_VAR} 标量节点替换为 os env 值；空串或未设置 → 报错。
func injectEnv(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		m := upperPlaceholderRe.FindStringSubmatch(node.Value)
		if m == nil {
			return nil
		}
		varName := m[1]
		val, ok := os.LookupEnv(varName)
		if !ok {
			return fmt.Errorf("env var %s not set (required by config placeholder ${%s})", varName, varName)
		}
		if val == "" {
			return fmt.Errorf("env var %s is empty (empty string treated as not provided)", varName)
		}
		node.Value = val
		node.Tag = "!!str"
	case yaml.MappingNode, yaml.SequenceNode:
		for _, child := range node.Content {
			if err := injectEnv(child); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateRequired 按 RequiredFields 驱动必填校验（反射按点路径取值判零）。
func validateRequired[T any](cfg *T, msgName string) error {
	required, ok := conf.RequiredFields[msgName]
	if !ok {
		return nil
	}
	v := reflect.ValueOf(cfg).Elem()
	for path := range required {
		val, err := getFieldByPath(v, path)
		if err != nil {
			return fmt.Errorf("required field %q: %w", path, err)
		}
		if isZero(val) {
			return fmt.Errorf("required field %q is missing or zero-value", path)
		}
	}
	return nil
}

// getFieldByPath 按点路径（如 "gate_cfg.node_id"）在 reflect.Value 中取字段。
func getFieldByPath(v reflect.Value, path string) (reflect.Value, error) {
	parts := strings.SplitN(path, ".", 2)
	fieldName := snakeToCamel(parts[0])
	f := v.FieldByName(fieldName)
	if !f.IsValid() {
		return reflect.Value{}, fmt.Errorf("field %q not found in struct", fieldName)
	}
	if f.Kind() == reflect.Ptr {
		if f.IsNil() {
			return reflect.Value{}, fmt.Errorf("field %q is nil pointer", fieldName)
		}
		f = f.Elem()
	}
	if len(parts) == 1 {
		return f, nil
	}
	return getFieldByPath(f, parts[1])
}

// snakeToCamel 将 snake_case 转为 CamelCase（与 gen_config.toCamel 一致）。
func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	var b strings.Builder
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		r := []rune(p)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	return b.String()
}

// isZero 判断零值：string→空串、数字→0、ptr/interface→nil、slice→空。
func isZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.String() == ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Ptr, reflect.Interface:
		return v.IsNil()
	case reflect.Slice:
		return v.Len() == 0
	case reflect.Bool:
		return false // bool required 无意义，不判零
	}
	return false
}

// checkStaticFieldsUnchanged 对比 old/new，非 ReloadableFields 字段变化则报错。
func checkStaticFieldsUnchanged[T any](old, newCfg *T) error {
	msgName := reflect.TypeOf(*old).Name()
	reloadable := conf.ReloadableFields[msgName]
	return compareStructs(
		reflect.ValueOf(old).Elem(),
		reflect.ValueOf(newCfg).Elem(),
		"", reloadable,
	)
}

func compareStructs(old, newV reflect.Value, prefix string, reloadable map[string]bool) error {
	t := old.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		yamlTag := field.Tag.Get("yaml")
		if yamlTag == "" || yamlTag == "-" {
			continue
		}
		key := strings.Split(yamlTag, ",")[0]
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		oldF, newF := old.Field(i), newV.Field(i)

		switch oldF.Kind() {
		case reflect.Ptr:
			if oldF.IsNil() != newF.IsNil() && !reloadable[path] {
				return fmt.Errorf("static field %q changed (nil mismatch)", path)
			}
			if !oldF.IsNil() && !newF.IsNil() {
				if err := compareStructs(oldF.Elem(), newF.Elem(), path, reloadable); err != nil {
					return err
				}
			}
		case reflect.Struct:
			if err := compareStructs(oldF, newF, path, reloadable); err != nil {
				return err
			}
		default:
			if !reflect.DeepEqual(oldF.Interface(), newF.Interface()) && !reloadable[path] {
				return fmt.Errorf("static field %q changed: %v → %v", path, oldF.Interface(), newF.Interface())
			}
		}
	}
	return nil
}
