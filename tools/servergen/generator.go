package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

const (
	kindStandard = "standard"
	kindSidecar  = "sidecar"
)

type GeneratorConfig struct {
	Name        string
	Pkg         string
	Kind        string
	RegisterEnv []string
	DryRun      bool
	Force       bool
}

type Generator struct {
	root string
	cfg  GeneratorConfig
	data templateData
}

type templateData struct {
	ServiceName string
	PackageName string
	Title       string
	ConfigType  string
	ConfigField string
	ServiceKind string
	CmdImport   string
	ConfigFile  string
}

type plannedFile struct {
	Path      string
	Content   []byte
	Overwrite bool
}

type plannedValueFile struct {
	Path      string
	Content   []byte
	Overwrite bool
}

type Plan struct {
	Files       []plannedFile
	ValueFiles  []plannedValueFile
}

func NewGenerator(cfg GeneratorConfig) (*Generator, error) {
	name := strings.TrimSpace(cfg.Name)
	if err := validateIdent(name); err != nil {
		return nil, fmt.Errorf("invalid service name %q: %w", name, err)
	}

	pkg := strings.TrimSpace(cfg.Pkg)
	if pkg == "" {
		pkg = derivePackageName(name)
	}
	if pkg == "" {
		return nil, errors.New("package name is empty")
	}
	if err := validateIdent(pkg); err != nil {
		return nil, fmt.Errorf("invalid package name %q: %w", pkg, err)
	}

	kind := strings.ToLower(strings.TrimSpace(cfg.Kind))
	if kind == "" {
		kind = kindStandard
	}
	if kind != kindStandard && kind != kindSidecar {
		return nil, fmt.Errorf("unsupported kind %q", cfg.Kind)
	}

	root, err := findProjectRoot()
	if err != nil {
		return nil, err
	}

	title := deriveTitle(name, pkg)
	data := templateData{
		ServiceName: name,
		PackageName: pkg,
		Title:       title,
		ConfigType:  title + "Config",
		ConfigField: title + "ConfigPath",
		ServiceKind: kind,
		CmdImport:   "project/internal/server/" + pkg,
		ConfigFile:  pkg + ".yaml",
	}

	return &Generator{root: root, cfg: cfg, data: data}, nil
}

func (g *Generator) Plan() (*Plan, error) {
	files := []fileSpec{
		{path: filepath.Join("cmd", g.data.ServiceName, "main.go"), tmpl: cmdMainTemplate},
		{path: filepath.Join("cmd", g.data.ServiceName, "CLAUDE.md"), tmpl: cmdClaudeTemplate},
		{path: filepath.Join("internal", "server", g.data.PackageName, "builder.go"), tmpl: serverBuilderTemplate},
		{path: filepath.Join("internal", "server", g.data.PackageName, "config.go"), tmpl: serverConfigTemplate},
		{path: filepath.Join("internal", "server", g.data.PackageName, "options.go"), tmpl: serverOptionsTemplate},
		{path: filepath.Join("internal", "server", g.data.PackageName, "CLAUDE.md"), tmpl: serverClaudeTemplate},
		{path: filepath.Join("config", "schema", g.data.PackageName+".proto"), tmpl: schemaTemplate},
		{path: filepath.Join("config", g.data.PackageName+".yaml"), tmpl: configTemplate},
	}

	plan := &Plan{}
	for _, spec := range files {
		content, err := renderTemplate(spec.tmpl, g.data)
		if err != nil {
			return nil, fmt.Errorf("render %s: %w", spec.path, err)
		}
		absPath := filepath.Join(g.root, spec.path)
		exists, err := pathExists(absPath)
		if err != nil {
			return nil, err
		}
		if exists && !g.cfg.Force {
			return nil, fmt.Errorf("target already exists: %s (use --force to overwrite)", spec.path)
		}
		plan.Files = append(plan.Files, plannedFile{
			Path:      spec.path,
			Content:   []byte(content),
			Overwrite: exists,
		})
	}

	if len(g.cfg.RegisterEnv) > 0 {
		for _, env := range g.cfg.RegisterEnv {
			valuePath := filepath.Join(g.root, "config", "values", env+".yaml")
			content, err := os.ReadFile(valuePath)
			if err != nil {
				return nil, fmt.Errorf("read values file %s: %w", filepath.Base(valuePath), err)
			}
			updated, changed, err := addServiceToSvrList(string(content), g.data.ServiceName)
			if err != nil {
				return nil, fmt.Errorf("update %s: %w", filepath.Base(valuePath), err)
			}
			if !changed {
				continue
			}
			plan.ValueFiles = append(plan.ValueFiles, plannedValueFile{
				Path:      filepath.Join("config", "values", env+".yaml"),
				Content:   []byte(updated),
				Overwrite: true,
			})
		}
	}

	return plan, nil
}

func (g *Generator) PrintPlan(plan *Plan) {
	fmt.Printf("servergen name=%s pkg=%s kind=%s root=%s\n", g.data.ServiceName, g.data.PackageName, g.data.ServiceKind, g.root)
	for _, file := range plan.Files {
		action := "create"
		if file.Overwrite {
			action = "overwrite"
		}
		fmt.Printf("%s %s\n", action, file.Path)
	}
	for _, file := range plan.ValueFiles {
		action := "update"
		if file.Overwrite {
			action = "overwrite"
		}
		fmt.Printf("%s %s\n", action, file.Path)
	}
}

func (g *Generator) Write(plan *Plan) error {
	for _, file := range plan.Files {
		if err := writeFile(filepath.Join(g.root, file.Path), file.Content); err != nil {
			return err
		}
	}
	for _, file := range plan.ValueFiles {
		if err := writeFile(filepath.Join(g.root, file.Path), file.Content); err != nil {
			return err
		}
	}
	return nil
}

type fileSpec struct {
	path string
	tmpl string
}

func renderTemplate(tmpl string, data templateData) (string, error) {
	t, err := template.New("servergen").Funcs(template.FuncMap{
		"eq": strings.EqualFold,
	}).Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not find project root with go.mod")
		}
		dir = parent
	}
}

func validateIdent(value string) error {
	if value == "" {
		return errors.New("empty identifier")
	}
	for i, r := range value {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			if i == 0 {
				return errors.New("identifier cannot start with a digit")
			}
			continue
		}
		return fmt.Errorf("contains invalid character %q", r)
	}
	return nil
}

func derivePackageName(name string) string {
	pkg := strings.TrimSuffix(name, "svr")
	if pkg == "" {
		pkg = name
	}
	return pkg
}

func deriveTitle(name, pkg string) string {
	base := pkg
	if base == "" {
		base = derivePackageName(name)
	}
	if base == "" {
		return ""
	}
	return strings.ToUpper(base[:1]) + base[1:]
}

func addServiceToSvrList(text, service string) (string, bool, error) {
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "svr_list:" {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false, errors.New("svr_list not found")
	}

	indent := leadingSpaces(lines[start])
	itemIndent := indent + "  "
	end := start + 1
	items := make([]string, 0, 8)
	for end < len(lines) {
		line := lines[end]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			end++
			continue
		}
		if !strings.HasPrefix(line, itemIndent+"-") {
			break
		}
		item := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if item == "" {
			end++
			continue
		}
		items = append(items, item)
		end++
	}
	if len(items) == 0 {
		return "", false, errors.New("svr_list is empty")
	}
	for _, item := range items {
		if item == service {
			return text, false, nil
		}
	}
	items = append(items, service)
	sort.Strings(items)

	rebuilt := make([]string, 0, len(lines)+2)
	rebuilt = append(rebuilt, lines[:start]...)
	rebuilt = append(rebuilt, indent+"svr_list:")
	for _, item := range items {
		rebuilt = append(rebuilt, itemIndent+"- "+item)
	}
	rebuilt = append(rebuilt, "")
	rebuilt = append(rebuilt, lines[end:]...)
	return strings.Join(rebuilt, "\n"), true, nil
}

func leadingSpaces(line string) string {
	for i, r := range line {
		if r != ' ' && r != '\t' {
			return line[:i]
		}
	}
	return line
}
