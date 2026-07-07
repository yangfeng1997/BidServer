package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"

	"project/pkg/configgen"
)

var (
	lowerPlaceholder = regexp.MustCompile(`\$\{([a-z][a-z0-9_]*)\}`)
	anyPlaceholder   = regexp.MustCompile(`\$\{([^}]+)\}`)
)

func main() {
	var (
		confDir    string
		svr        string
		env        string
		outDir     string
		commonOnly bool
	)
	flag.StringVar(&confDir, "conf", "conf", "配置根目录")
	flag.StringVar(&svr, "svr", "", "目标服务名")
	flag.StringVar(&env, "env", "dev", "部署环境（dev/test/prod）")
	flag.StringVar(&outDir, "out", "", "输出目录（默认 run/{svr}/conf）")
	flag.BoolVar(&commonOnly, "common", false, "只烘焙 common.yaml")
	flag.Parse()

	values := loadValues(filepath.Join(confDir, "values", env+".yaml"))

	if commonOnly {
		bakeOnce(filepath.Join(confDir, "common", "common.yaml"),
			filepath.Join("run", "common", "conf", "common.yaml"), values)
		return
	}

	if svr == "" {
		fmt.Fprintln(os.Stderr, "--svr or --common is required")
		os.Exit(1)
	}
	if outDir == "" {
		outDir = filepath.Join("run", svr, "conf")
	}

	svrTmplDir := filepath.Join(confDir, "servers", svr)
	yamls, err := filepath.Glob(filepath.Join(svrTmplDir, "*.yaml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "glob %s: %v\n", svrTmplDir, err)
		os.Exit(1)
	}
	for _, tmplPath := range yamls {
		base := filepath.Base(tmplPath)
		outPath := filepath.Join(outDir, base)
		bakeOnce(tmplPath, outPath, values)
	}
}

// loadValues 从 yaml 文件加载值（递归展平，点路径作 key）
func loadValues(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: cannot read values file %s: %v\n", path, err)
		os.Exit(1)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: parse %s: %v\n", path, err)
		os.Exit(1)
	}
	out := make(map[string]string)
	flatten(raw, "", out)
	return out
}

func flatten(m map[string]any, prefix string, out map[string]string) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]any:
			flatten(val, key, out)
		case string:
			out[key] = val
		default:
			out[key] = fmt.Sprint(val)
		}
	}
}

func bakeOnce(tmplPath, outPath string, values map[string]string) {
	if err := bakeYAML(tmplPath, outPath, values); err != nil {
		fmt.Fprintf(os.Stderr, "bake %s: %v\n", tmplPath, err)
		os.Exit(1)
	}
	fmt.Printf("baked %s → %s\n", filepath.Base(tmplPath), outPath)
}

func bakeYAML(tmplPath, outPath string, values map[string]string) error {
	data, err := os.ReadFile(tmplPath)
	if err != nil {
		return err
	}
	text := string(data)

	if ms := findMixedPlaceholders(text); len(ms) > 0 {
		return fmt.Errorf("mixed-case placeholders not allowed: %v", ms)
	}

	var missing []string
	text = lowerPlaceholder.ReplaceAllStringFunc(text, func(token string) string {
		key := token[2 : len(token)-1]
		if val, ok := values[key]; ok {
			return val
		}
		missing = append(missing, key)
		return token
	})
	if len(missing) > 0 {
		return fmt.Errorf("placeholder(s) not found in values: %v", missing)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(outPath, []byte(text), 0o644)
}

func findMixedPlaceholders(text string) []string {
	var out []string
	for _, m := range anyPlaceholder.FindAllString(text, -1) {
		inner := m[2 : len(m)-1]
		lower, upper := configgen.ClassifyPlaceholder(inner)
		if lower && upper {
			out = append(out, m)
		}
	}
	return out
}
