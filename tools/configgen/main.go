package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type fieldInfo struct {
	GoName     string
	GoType     string
	YamlName   string
	Message    bool
	Repeated   bool
	Required   bool
	Reload     bool
	Env        bool
	EnumValues []string
}

type messageInfo struct {
	Name   string
	Fields []fieldInfo
	Root   bool
}

func main() {
	var (
		schemaDir string
		outDir    string
	)
	flag.StringVar(&schemaDir, "schema", "config/schema", "schema proto directory")
	flag.StringVar(&outDir, "out", "config/gen", "generated config output directory")
	flag.Parse()

	messages, err := parseSchemaDir(schemaDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(messages) == 0 {
		fmt.Fprintln(os.Stderr, "no config messages found")
		os.Exit(1)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", outDir, err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(outDir, "config.go"), []byte(renderConfig(messages)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write config.go: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(outDir, "validate.go"), []byte(renderValidate(messages)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write validate.go: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(outDir, "loader.go"), []byte(renderLoader(messages)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write loader.go: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(outDir, "source.go"), []byte(renderSource(messages)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write source.go: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(outDir, "reload.go"), []byte(renderReload(messages)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write reload.go: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("generated %d config messages into %s\n", len(messages), outDir)
}

func parseSchemaDir(schemaDir string) ([]messageInfo, error) {
	files, err := filepath.Glob(filepath.Join(schemaDir, "*.proto"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var messages []messageInfo
	for _, path := range files {
		if filepath.Base(path) == "options.proto" {
			continue
		}
		parsed, err := parseProto(path)
		if err != nil {
			return nil, err
		}
		messages = append(messages, parsed...)
	}
	return messages, nil
}

func parseProto(path string) ([]messageInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	var messages []messageInfo
	for i := 0; i < len(lines); i++ {
		line := stripComment(strings.TrimSpace(lines[i]))
		if !strings.HasPrefix(line, "message ") || !strings.Contains(line, "{") {
			continue
		}
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "message "), "{"))
		msg := messageInfo{Name: name, Root: isRootConfig(name)}
		for i++; i < len(lines); i++ {
			fieldLine := stripComment(strings.TrimSpace(lines[i]))
			if fieldLine == "}" {
				break
			}
			if fieldLine == "" {
				continue
			}
			field, ok, err := parseField(fieldLine)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if ok {
				msg.Fields = append(msg.Fields, field)
			}
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func isRootConfig(name string) bool {
	switch name {
	case "CommonConfig", "GateConfig", "LobbyConfig":
		return true
	default:
		return false
	}
}

func stripComment(line string) string {
	if idx := strings.Index(line, "//"); idx >= 0 {
		line = line[:idx]
	}
	return strings.TrimSpace(line)
}

func parseField(line string) (fieldInfo, bool, error) {
	line = strings.TrimSuffix(line, ";")
	if line == "" || strings.HasPrefix(line, "option ") || strings.HasPrefix(line, "import ") {
		return fieldInfo{}, false, nil
	}
	options := ""
	if idx := strings.Index(line, "["); idx >= 0 {
		options = line[idx:]
		line = strings.TrimSpace(line[:idx])
	}
	if idx := strings.Index(line, "="); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return fieldInfo{}, false, nil
	}
	repeated := false
	if parts[0] == "repeated" {
		repeated = true
		parts = parts[1:]
	}
	protoType := parts[0]
	protoName := parts[1]
	field := fieldInfo{
		GoName:   snakeToPascal(protoName),
		YamlName: protoName,
		Repeated: repeated,
	}
	field.GoType, field.Message = goType(protoType, repeated)
	field.Required = strings.Contains(options, "config.required")
	field.Reload = strings.Contains(options, "config.reload")
	field.Env = strings.Contains(options, "config.env")
	if field.Reload && field.Env {
		return fieldInfo{}, false, fmt.Errorf("field %s cannot set both reload and env", protoName)
	}
	field.EnumValues = parseEnumValues(options)
	return field, true, nil
}

func goType(protoType string, repeated bool) (string, bool) {
	base := ""
	message := false
	switch protoType {
	case "string":
		base = "string"
	case "int32", "sint32", "sfixed32":
		base = "int32"
	case "int64", "sint64", "sfixed64":
		base = "int64"
	case "uint32", "fixed32":
		base = "uint32"
	case "uint64", "fixed64":
		base = "uint64"
	case "bool":
		base = "bool"
	case "float":
		base = "float32"
	case "double":
		base = "float64"
	default:
		base = protoType
		message = true
	}
	if repeated {
		return "[]" + base, message
	}
	return base, message
}

func parseEnumValues(options string) []string {
	needle := "config.enum_values) = \""
	idx := strings.Index(options, needle)
	if idx < 0 {
		return nil
	}
	start := idx + len(needle)
	end := strings.Index(options[start:], "\"")
	if end < 0 {
		return nil
	}
	parts := strings.Split(options[start:start+end], ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func renderConfig(messages []messageInfo) string {
	var b strings.Builder
	b.WriteString("// Code generated by tools/configgen. DO NOT EDIT.\n")
	b.WriteString("package configgen\n\n")
	for _, msg := range messages {
		b.WriteString("type ")
		b.WriteString(msg.Name)
		b.WriteString(" struct {\n")
		for _, field := range msg.Fields {
			b.WriteString("\t")
			b.WriteString(field.GoName)
			b.WriteString(" ")
			b.WriteString(field.GoType)
			b.WriteString(" `yaml:\"")
			b.WriteString(field.YamlName)
			b.WriteString("\"`\n")
		}
		b.WriteString("}\n\n")
	}
	return b.String()
}

func renderValidate(messages []messageInfo) string {
	var b strings.Builder
	b.WriteString("// Code generated by tools/configgen. DO NOT EDIT.\n")
	b.WriteString("package configgen\n\n")
	b.WriteString("import \"fmt\"\n\n")
	for _, msg := range messages {
		b.WriteString("func (cfg *")
		b.WriteString(msg.Name)
		b.WriteString(") Validate() error {\n")
		b.WriteString("\tif cfg == nil {\n\t\treturn fmt.Errorf(\"config is nil\")\n\t}\n")
		for _, field := range msg.Fields {
			writeFieldValidation(&b, field, field.YamlName)
		}
		b.WriteString("\treturn nil\n}\n\n")
	}
	return b.String()
}

func renderLoader(messages []messageInfo) string {
	var b strings.Builder
	b.WriteString("// Code generated by tools/configgen. DO NOT EDIT.\n")
	b.WriteString("package configgen\n\n")
	b.WriteString("import (\n")
	b.WriteString("\t\"fmt\"\n\n")
	b.WriteString("\t\"project/internal/core/config\"\n")
	b.WriteString(")\n\n")
	for _, msg := range messages {
		if !msg.Root {
			continue
		}
		name := strings.TrimSuffix(msg.Name, "Config")
		key := strings.ToLower(name)
		b.WriteString("func Load")
		b.WriteString(name)
		b.WriteString("(path string) (*")
		b.WriteString(msg.Name)
		b.WriteString(", error) {\n")
		b.WriteString("\tcfg, err := config.LoadYAML[*")
		b.WriteString(msg.Name)
		b.WriteString("](path)\n")
		b.WriteString("\tif err != nil {\n\t\treturn nil, fmt.Errorf(\"load ")
		b.WriteString(key)
		b.WriteString(" config: %w\", err)\n\t}\n")
		b.WriteString("\tif err := cfg.Validate(); err != nil {\n\t\treturn nil, fmt.Errorf(\"validate ")
		b.WriteString(key)
		b.WriteString(" config: %w\", err)\n\t}\n")
		b.WriteString("\treturn cfg, nil\n}\n\n")
	}
	return b.String()
}

func renderSource(messages []messageInfo) string {
	roots := rootMessages(messages)
	var b strings.Builder
	b.WriteString("// Code generated by tools/configgen. DO NOT EDIT.\n")
	b.WriteString("package configgen\n\n")
	b.WriteString("import \"project/internal/core/config\"\n\n")
	for _, msg := range roots {
		name := strings.TrimSuffix(msg.Name, "Config")
		b.WriteString("func New")
		b.WriteString(name)
		b.WriteString("ConfigEntry(path string) (*config.ConfigEntry[")
		b.WriteString(msg.Name)
		b.WriteString("], error) {\n")
		b.WriteString("\treturn config.NewConfigEntry(path, Load")
		b.WriteString(name)
		b.WriteString(", Check")
		b.WriteString(name)
		b.WriteString("Reload)\n")
		b.WriteString("}\n\n")
	}
	return b.String()
}

func renderReload(messages []messageInfo) string {
	roots := rootMessages(messages)
	msgMap := messageMap(messages)
	var b strings.Builder
	b.WriteString("// Code generated by tools/configgen. DO NOT EDIT.\n")
	b.WriteString("package configgen\n\n")
	b.WriteString("import (\n")
	b.WriteString("\t\"fmt\"\n")
	b.WriteString("\t\"reflect\"\n")
	b.WriteString(")\n\n")
	for _, msg := range roots {
		name := strings.TrimSuffix(msg.Name, "Config")
		b.WriteString("func Check")
		b.WriteString(name)
		b.WriteString("Reload(candidate *")
		b.WriteString(msg.Name)
		b.WriteString(", current *")
		b.WriteString(msg.Name)
		b.WriteString(") error {\n")
		b.WriteString("\tif candidate == nil || current == nil {\n\t\treturn nil\n\t}\n")
		writeReloadChecks(&b, msg, msgMap, "candidate", "current", "")
		b.WriteString("\treturn nil\n}\n\n")
	}
	return b.String()
}

func writeFieldValidation(b *strings.Builder, field fieldInfo, yamlName string) {
	if field.Message && !field.Repeated {
		b.WriteString("\tif err := cfg.")
		b.WriteString(field.GoName)
		b.WriteString(".Validate(); err != nil {\n")
		b.WriteString("\t\treturn fmt.Errorf(\"")
		b.WriteString(yamlName)
		b.WriteString(": %w\", err)\n\t}\n")
		return
	}
	if field.Required {
		expr := requiredExpr("cfg."+field.GoName, field.GoType, field.Repeated)
		if expr != "" {
			b.WriteString("\tif ")
			b.WriteString(expr)
			b.WriteString(" {\n\t\treturn fmt.Errorf(\"")
			b.WriteString(yamlName)
			b.WriteString(" is required\")\n\t}\n")
		}
	}
	if len(field.EnumValues) > 0 && field.GoType == "string" {
		b.WriteString("\tif cfg.")
		b.WriteString(field.GoName)
		b.WriteString(" != \"\" {\n\t\tswitch cfg.")
		b.WriteString(field.GoName)
		b.WriteString(" {\n\t\tcase ")
		for i, value := range field.EnumValues {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(fmt.Sprintf("%q", value))
		}
		b.WriteString(":\n\t\tdefault:\n\t\t\treturn fmt.Errorf(\"")
		b.WriteString(yamlName)
		b.WriteString("=%q is invalid\", cfg.")
		b.WriteString(field.GoName)
		b.WriteString(")\n\t\t}\n\t}\n")
	}
}

func writeReloadChecks(b *strings.Builder, msg messageInfo, msgMap map[string]messageInfo, candidate, current, prefix string) {
	for _, field := range msg.Fields {
		path := field.YamlName
		if prefix != "" {
			path = prefix + "." + field.YamlName
		}
		candidateExpr := candidate + "." + field.GoName
		currentExpr := current + "." + field.GoName
		if field.Message && !field.Repeated {
			child, ok := msgMap[field.GoType]
			if ok {
				writeReloadChecks(b, child, msgMap, candidateExpr, currentExpr, path)
			}
			continue
		}
		if field.Reload {
			continue
		}
		b.WriteString("\tif !reflect.DeepEqual(")
		b.WriteString(candidateExpr)
		b.WriteString(", ")
		b.WriteString(currentExpr)
		b.WriteString(") {\n")
		b.WriteString("\t\treturn fmt.Errorf(\"")
		b.WriteString(path)
		b.WriteString(" cannot reload\")\n\t}\n")
	}
}

func rootMessages(messages []messageInfo) []messageInfo {
	var roots []messageInfo
	for _, msg := range messages {
		if msg.Root {
			roots = append(roots, msg)
		}
	}
	return roots
}

func messageMap(messages []messageInfo) map[string]messageInfo {
	out := make(map[string]messageInfo, len(messages))
	for _, msg := range messages {
		out[msg.Name] = msg
	}
	return out
}

func requiredExpr(name, goType string, repeated bool) string {
	if repeated {
		return "len(" + name + ") == 0"
	}
	switch goType {
	case "string":
		return name + " == \"\""
	case "int32", "int64", "uint32", "uint64", "float32", "float64":
		return name + " == 0"
	}
	return ""
}

func snakeToPascal(s string) string {
	parts := strings.Split(s, "_")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, "")
}
