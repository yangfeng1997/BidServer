package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// fieldInfo holds parsed info for one proto field
type fieldInfo struct {
	ProtoName   string // snake_case: "listen_tcp"
	GoName      string // PascalCase: "ListenTcp"
	GoType      string // e.g. "string", "*GateConfig", "[]string"
	MessageType string // nested message name if TYPE_MESSAGE, e.g. "GateConfig"
	Repeated    bool
	Required    bool
	Env         bool
	Reload      bool
	EnumValues  string
}

// msgInfo holds parsed info for one proto message
type msgInfo struct {
	Name          string      // e.g. "GatesvrConfig"
	Fields        []fieldInfo
	SourceFile    string // proto 文件名，e.g. "gatesvr.proto"
	IsServiceRoot bool   // 是否为服务 proto 的顶层 message（生成 Loader 目标）
}

func main() {
	var (
		descriptorFile string
		outDir         string
	)
	flag.StringVar(&descriptorFile, "descriptor", "conf/schema/gen/config.pb.descriptor", "FileDescriptorSet 路径")
	flag.StringVar(&outDir, "out", "conf/schema/gen", "输出目录")
	flag.Parse()

	data, err := os.ReadFile(descriptorFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read descriptor: %v\n", err)
		os.Exit(1)
	}

	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(data, fds); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal descriptor: %v\n", err)
		os.Exit(1)
	}

	msgs, _ := collectMessages(fds)
	if len(msgs) == 0 {
		fmt.Fprintln(os.Stderr, "no messages found in descriptor set")
		os.Exit(1)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", outDir, err)
		os.Exit(1)
	}

	configPath := filepath.Join(outDir, "config.go")
	if err := os.WriteFile(configPath, []byte(renderConfig(msgs)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", configPath, err)
		os.Exit(1)
	}
	fmt.Println("generated", configPath)

	validatePath := filepath.Join(outDir, "validate.go")
	if err := os.WriteFile(validatePath, []byte(renderValidate(msgs)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", validatePath, err)
		os.Exit(1)
	}
	fmt.Println("generated", validatePath)

	diffPath := filepath.Join(outDir, "diff.go")
	if err := os.WriteFile(diffPath, []byte(renderDiff(msgs)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", diffPath, err)
		os.Exit(1)
	}
	fmt.Println("generated", diffPath)

	// 为每个 service root message 生成 Loader.gen.go
	loaderCount := renderLoaders(msgs)
	if loaderCount > 0 {
		fmt.Printf("generated %d service loaders\n", loaderCount)
	}

	// 为 CommonConfig 生成 Loader（不热更，精简版）
	if renderCommonLoader(msgs) {
		fmt.Println("generated common loader")
	}
}

// collectMessages 从 FileDescriptorSet 递归收集所有 message（含嵌套），去重后返回
func collectMessages(fds *descriptorpb.FileDescriptorSet) (msgs []msgInfo, serviceRoots map[string]bool) {
	isCommon := map[string]bool{}
	serviceRoots = map[string]bool{}
	var allMsgs []msgInfo

	// 识别服务 proto：非 common/types/options/google 的 .proto 文件
	isServiceProto := func(fn string) bool {
		if strings.HasPrefix(fn, "google/protobuf/") {
			return false
		}
		base := filepath.Base(fn)
		switch base {
		case "options.proto", "common.proto", "types.proto":
			return false
		}
		return true
	}

	for _, file := range fds.GetFile() {
		fn := file.GetName()
		if strings.HasPrefix(fn, "google/protobuf/") || strings.HasSuffix(fn, "options.proto") {
			continue
		}
		isCommonFile := strings.HasSuffix(fn, "common.proto") || filepath.Base(fn) == "common.proto"
		if msgs := file.GetMessageType(); len(msgs) > 0 && isServiceProto(fn) {
			// 服务 proto 的顶层 message 是 service root
			serviceRoots[msgs[0].GetName()] = true
		}
		for _, msg := range file.GetMessageType() {
			collectRecursive(msg, fn, isCommonFile, &allMsgs)
		}
	}

	// 去重：按 message Name 去重（首个出现保留其 SourceFile）
	seen := map[string]bool{}
	for _, mi := range allMsgs {
		if seen[mi.Name] {
			continue
		}
		seen[mi.Name] = true
		// 标记 service root
		if serviceRoots[mi.Name] {
			mi.IsServiceRoot = true
		}
		msgs = append(msgs, mi)
	}

	// 稳定排序：common.proto 的 message 在前
	sort.Slice(msgs, func(i, j int) bool {
		iCommon := isCommon[msgs[i].Name]
		jCommon := isCommon[msgs[j].Name]
		if iCommon != jCommon {
			return iCommon
		}
		return msgs[i].Name < msgs[j].Name
	})

	return
}

// collectRecursive 递归收集 message 及其嵌套 message
func collectRecursive(msg *descriptorpb.DescriptorProto, sourceFile string, commonFile bool, out *[]msgInfo) {
	mi := parseMessage(msg)
	mi.SourceFile = sourceFile
	*out = append(*out, mi)

	for _, nested := range msg.GetNestedType() {
		collectRecursive(nested, sourceFile, commonFile, out)
	}
}

// parseMessage 从 MessageDescriptorProto 解析 message 信息
func parseMessage(msg *descriptorpb.DescriptorProto) msgInfo {
	mi := msgInfo{Name: msg.GetName()}
	for _, field := range msg.GetField() {
		fi := parseField(field)
		mi.Fields = append(mi.Fields, fi)
	}
	return mi
}

// parseField 从 FieldDescriptorProto 解析字段信息
func parseField(field *descriptorpb.FieldDescriptorProto) fieldInfo {
	protoName := field.GetName()
	goName := snakeToPascal(protoName)

	repeated := field.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED
	reload, required, env, enumValues := false, false, false, ""

	if opts := field.GetOptions(); opts != nil {
		raw, err := proto.Marshal(opts)
		if err == nil {
			reload, required, env, enumValues = parseCustomOptions(raw)
		}
	}

	// 校验：env=true 只能用于 string 字段
	if env && field.GetType() != descriptorpb.FieldDescriptorProto_TYPE_STRING {
		fmt.Fprintf(os.Stderr, "ERROR: field %q: env=true requires string type, got %v\n",
			protoName, field.GetType())
		os.Exit(1)
	}

	goType, msgType := protoTypeToGo(field.GetType(), repeated, field.GetTypeName())

	return fieldInfo{
		ProtoName:   protoName,
		GoName:      goName,
		GoType:      goType,
		MessageType: msgType,
		Repeated:    repeated,
		Required:    required,
		Env:         env,
		Reload:      reload,
		EnumValues:  enumValues,
	}
}

// parseCustomOptions 从序列化的 FieldOptions bytes 中提取自定义 option（50005-50008）
func parseCustomOptions(raw []byte) (reload, required, env bool, enumValues string) {
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			break
		}
		raw = raw[n:]
		switch num {
		case 50005: // reload (bool)
			v, n := protowire.ConsumeVarint(raw)
			if n >= 0 {
				reload = v != 0
				raw = raw[n:]
			}
		case 50006: // required (bool)
			v, n := protowire.ConsumeVarint(raw)
			if n >= 0 {
				required = v != 0
				raw = raw[n:]
			}
		case 50007: // env (bool)
			v, n := protowire.ConsumeVarint(raw)
			if n >= 0 {
				env = v != 0
				raw = raw[n:]
			}
		case 50008: // enum_values (string)
			b, n := protowire.ConsumeBytes(raw)
			if n >= 0 {
				enumValues = string(b)
				raw = raw[n:]
			}
		default:
			n = protowire.ConsumeFieldValue(num, typ, raw)
			if n < 0 {
				return
			}
			raw = raw[n:]
		}
	}
	return
}

// protoTypeToGo 将 proto 字段类型映射为 Go 类型
func protoTypeToGo(pt descriptorpb.FieldDescriptorProto_Type, repeated bool, typeName string) (goType, msgType string) {
	var scalar string
	switch pt {
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		scalar = "bool"
	case descriptorpb.FieldDescriptorProto_TYPE_INT32, descriptorpb.FieldDescriptorProto_TYPE_SINT32, descriptorpb.FieldDescriptorProto_TYPE_SFIXED32:
		scalar = "int32"
	case descriptorpb.FieldDescriptorProto_TYPE_INT64, descriptorpb.FieldDescriptorProto_TYPE_SINT64, descriptorpb.FieldDescriptorProto_TYPE_SFIXED64:
		scalar = "int64"
	case descriptorpb.FieldDescriptorProto_TYPE_UINT32, descriptorpb.FieldDescriptorProto_TYPE_FIXED32:
		scalar = "uint32"
	case descriptorpb.FieldDescriptorProto_TYPE_UINT64, descriptorpb.FieldDescriptorProto_TYPE_FIXED64:
		scalar = "uint64"
	case descriptorpb.FieldDescriptorProto_TYPE_FLOAT:
		scalar = "float32"
	case descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		scalar = "float64"
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		scalar = "string"
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		scalar = "[]byte"
	case descriptorpb.FieldDescriptorProto_TYPE_ENUM:
		// proto enum: extract type name for alias, default to int32
		scalar = "int32"
		if typeName != "" {
			if idx := strings.LastIndex(typeName, "."); idx >= 0 {
				scalar = strings.ToLower(typeName[idx+1:]) // enum type name as comment-hint
			}
		}
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE:
		msgType = typeName
		if idx := strings.LastIndex(msgType, "."); idx >= 0 {
			msgType = msgType[idx+1:]
		}
		if repeated {
			goType = "[]*" + msgType
		} else {
			goType = "*" + msgType
		}
		return
	default:
		scalar = "int32"
	}
	if repeated {
		goType = "[]" + scalar
	} else {
		goType = scalar
	}
	return
}

// snakeToPascal 将 snake_case 转为 PascalCase
func snakeToPascal(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

// ── 代码生成 ──

func renderValidate(msgs []msgInfo) string {
	var b strings.Builder
	b.WriteString("// Code generated by gen_config. DO NOT EDIT.\n")
	b.WriteString("package gen\n\n")
	b.WriteString("import (\n\t\"fmt\"\n\t\"strings\"\n)\n\n")

	// prefixAll helper
	b.WriteString("func prefixAll(errs []string, p string) []string {\n")
	b.WriteString("\tfor i := range errs {\n")
	b.WriteString("\t\terrs[i] = p + errs[i]\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn errs\n")
	b.WriteString("}\n\n")

	for _, msg := range msgs {
		renderValidateMethod(&b, msg)
	}
	return b.String()
}

func renderValidateMethod(b *strings.Builder, msg msgInfo) {
	b.WriteString("func (c *")
	b.WriteString(msg.Name)
	b.WriteString(") Validate() []string {\n")
	b.WriteString("\tvar errs []string\n")
	b.WriteString("\tif c == nil { return []string{\"<nil>\"} }\n")

	for _, f := range msg.Fields {
		isMsg := f.MessageType != "" && !f.Repeated
		isRepeatedMsg := f.MessageType != "" && f.Repeated

		if isMsg {
			// 嵌套 message 字段（*SubType）
			b.WriteString("\tif c.")
			b.WriteString(f.GoName)
			b.WriteString(" != nil {\n")
			b.WriteString("\t\terrs = append(errs, prefixAll(c.")
			b.WriteString(f.GoName)
			b.WriteString(".Validate(), \"")
			b.WriteString(f.ProtoName)
			b.WriteString(".\")...)\n")
			b.WriteString("\t}\n")
			if f.Required {
				b.WriteString("\tif c.")
				b.WriteString(f.GoName)
				b.WriteString(" == nil { errs = append(errs, \"")
				b.WriteString(f.ProtoName)
				b.WriteString(" is required\") }\n")
			}
		} else if isRepeatedMsg {
			// 嵌套 message 切片（[]*SubType）：逐个校验，整体 required
			if f.Required {
				b.WriteString("\tif len(c.")
				b.WriteString(f.GoName)
				b.WriteString(") == 0 { errs = append(errs, \"")
				b.WriteString(f.ProtoName)
				b.WriteString(" is required\") }\n")
			}
		} else {
			// 标量或标量切片
			isSlice := f.Repeated
			zeroCheck := zeroExpr(f.GoName, f.GoType, isSlice)
			if f.Required && zeroCheck != "" {
				b.WriteString("\tif ")
				b.WriteString(zeroCheck)
				b.WriteString(" { errs = append(errs, \"")
				b.WriteString(f.ProtoName)
				b.WriteString(" is required\") }\n")
			}

			// enum 检查（仅 string 标量字段）
			if f.EnumValues != "" && f.GoType == "string" {
				vals := parseEnumValues(f.EnumValues)
				b.WriteString("\tif c.")
				b.WriteString(f.GoName)
				b.WriteString(` != ""`)
				// 值不在列表 → 报错
				b.WriteString(" {\n")
				b.WriteString("\t\tswitch c.")
				b.WriteString(f.GoName)
				b.WriteString(" {\n")
				b.WriteString("\t\tcase ")
				for i, v := range vals {
					if i > 0 {
						b.WriteString(", ")
					}
					b.WriteString(fmt.Sprintf("%q", v))
				}
				b.WriteString(":\n")
				b.WriteString("\t\tdefault:\n")
				b.WriteString("\t\t\terrs = append(errs, fmt.Sprintf(\"")
				b.WriteString(f.ProtoName)
				b.WriteString("=%q not in [")
				for i, v := range vals {
					if i > 0 {
						b.WriteString(" ")
					}
					b.WriteString(v)
				}
				b.WriteString("]\", c.")
				b.WriteString(f.GoName)
				b.WriteString("))\n")
				b.WriteString("\t\t}\n")
				b.WriteString("\t}\n")
			}
			// env 检查：字段标记 env=true 时，值不得残留 ${...} 占位符
			if f.Env && f.GoType == "string" {
				b.WriteString("\tif c.")
				b.WriteString(f.GoName)
				b.WriteString(` != "" && strings.Contains(c.`)
				b.WriteString(f.GoName)
				b.WriteString(`, "${") { errs = append(errs, "`)
				b.WriteString(f.ProtoName)
				b.WriteString(` env var not injected: "+c.`)
				b.WriteString(f.GoName)
				b.WriteString(") }\n")
			}
		}
	}

	b.WriteString("\treturn errs\n}\n\n")
}

func zeroExpr(goName, goType string, isSlice bool) string {
	if isSlice {
		return fmt.Sprintf("len(c.%s) == 0", goName)
	}
	switch goType {
	case "string":
		return fmt.Sprintf("c.%s == \"\"", goName)
	case "int32", "int64", "uint32", "uint64", "int", "float32", "float64":
		return fmt.Sprintf("c.%s == 0", goName)
	}
	// bool / []byte 等：不生成 required 检查（bool false 有意为之）
	return ""
}

func parseEnumValues(s string) []string {
	parts := strings.Split(s, ",")
	var vals []string
	for _, p := range parts {
		vals = append(vals, strings.TrimSpace(p))
	}
	return vals
}

func renderDiff(msgs []msgInfo) string {
	var b strings.Builder
	b.WriteString("// Code generated by gen_config. DO NOT EDIT.\n")
	b.WriteString("package gen\n\n")
	b.WriteString("import \"reflect\"\n\n")

	for _, msg := range msgs {
		renderCheckStatic(&b, msg)
	}
	return b.String()
}

func renderCheckStatic(b *strings.Builder, msg msgInfo) {
	b.WriteString("func (c *")
	b.WriteString(msg.Name)
	b.WriteString(") CheckStatic(old *")
	b.WriteString(msg.Name)
	b.WriteString(") []string {\n")
	b.WriteString("\tvar stale []string\n")
	b.WriteString("\tif old == nil || c == nil { return []string{\"<nil>\"} }\n")

	for _, f := range msg.Fields {
		if f.Reload {
			continue // reload 字段允许热更，跳过
		}
		isMsg := f.MessageType != "" && !f.Repeated
		isRepeatedMsg := f.MessageType != "" && f.Repeated

		if isMsg {
			// 嵌套 message 字段（*SubType）：递归，加前缀；nil 时处理为整体变更
			b.WriteString("\tif c.")
			b.WriteString(f.GoName)
			b.WriteString(" != nil && old.")
			b.WriteString(f.GoName)
			b.WriteString(" != nil {\n")
			b.WriteString("\t\tstale = append(stale, prefixAll(c.")
			b.WriteString(f.GoName)
			b.WriteString(".CheckStatic(old.")
			b.WriteString(f.GoName)
			b.WriteString("), \"")
			b.WriteString(f.ProtoName)
			b.WriteString(".\")...)\n")
			b.WriteString("\t} else if c.")
			b.WriteString(f.GoName)
			b.WriteString(" != old.")
			b.WriteString(f.GoName)
			b.WriteString(" {\n")
			b.WriteString("\t\tstale = append(stale, \"")
			b.WriteString(f.ProtoName)
			b.WriteString("\")\n")
			b.WriteString("\t}\n")
		} else if isRepeatedMsg {
			// 嵌套 message 切片（[]*SubType）：DeepEqual 整体比较
			b.WriteString("\tif !reflect.DeepEqual(c.")
			b.WriteString(f.GoName)
			b.WriteString(", old.")
			b.WriteString(f.GoName)
			b.WriteString(") { stale = append(stale, \"")
			b.WriteString(f.ProtoName)
			b.WriteString("\") }\n")
		} else if f.Repeated {
			// 标量切片：DeepEqual 比较
			b.WriteString("\tif !reflect.DeepEqual(c.")
			b.WriteString(f.GoName)
			b.WriteString(", old.")
			b.WriteString(f.GoName)
			b.WriteString(") { stale = append(stale, \"")
			b.WriteString(f.ProtoName)
			b.WriteString("\") }\n")
		} else {
			// 标量字段：直接 != 比较
			b.WriteString("\tif c.")
			b.WriteString(f.GoName)
			b.WriteString(" != old.")
			b.WriteString(f.GoName)
			b.WriteString(" { stale = append(stale, \"")
			b.WriteString(f.ProtoName)
			b.WriteString("\") }\n")
		}
	}

	b.WriteString("\treturn stale\n}\n\n")
}

func renderConfig(msgs []msgInfo) string {
	var b strings.Builder
	b.WriteString("// Code generated by gen_config. DO NOT EDIT.\n")
	b.WriteString("package gen\n\n")

	for _, msg := range msgs {
		renderStruct(&b, msg)
	}
	return b.String()
}

func renderStruct(b *strings.Builder, msg msgInfo) {
	b.WriteString("// ")
	b.WriteString(msg.Name)
	b.WriteString(" 配置结构\n")
	b.WriteString("type ")
	b.WriteString(msg.Name)
	b.WriteString(" struct {\n")
	for _, f := range msg.Fields {
		b.WriteString("\t")
		b.WriteString(f.GoName)
		b.WriteString(" ")
		b.WriteString(f.GoType)
		b.WriteString(" `yaml:\"")
		b.WriteString(f.ProtoName)
		b.WriteString("\"`\n")
	}
	b.WriteString("}\n\n")
}

// renderLoaders 为每个 service root message 生成 typed Loader 到 conf/schema/gen/loader/
func renderLoaders(msgs []msgInfo) int {
	dir := filepath.Join("conf", "schema", "gen", "loader")
	os.MkdirAll(dir, 0o755)
	count := 0
	for _, msg := range msgs {
		if !msg.IsServiceRoot {
			continue
		}
		configType := msg.Name                       // "GatesvrConfig"
		prefix := strings.TrimSuffix(configType, "Config")   // "Gatesvr"
		svcName := svcNameFromProto(msg.SourceFile)           // "gatesvr"

		var b strings.Builder
		b.WriteString("// Code generated by gen_config. DO NOT EDIT.\n")
		b.WriteString("package config\n\n")

		b.WriteString("import (\n")
		b.WriteString("\t\"fmt\"\n")
		b.WriteString("\t\"os\"\n")
		b.WriteString("\t\"sync/atomic\"\n\n")
		b.WriteString("\t\"gopkg.in/yaml.v3\"\n\n")
		b.WriteString("\t\"project/conf/schema/gen\"\n")
		b.WriteString("\tconfig \"project/internal/core/config\"\n")
		b.WriteString("\t\"project/pkg/configgen\"\n")
		b.WriteString(")\n\n")

		// Loader struct
		b.WriteString("type ")
		b.WriteString(prefix)
		b.WriteString("Loader struct {\n")
		b.WriteString("\tfiles  []string\n")
		b.WriteString("\tshadow *gen.")
		b.WriteString(configType)
		b.WriteString("\n")
		b.WriteString("\tcur    atomic.Pointer[gen.")
		b.WriteString(configType)
		b.WriteString("]\n")
		b.WriteString("}\n\n")

		// singleton
		b.WriteString("var ")
		b.WriteString(svcName)
		b.WriteString(" *")
		b.WriteString(prefix)
		b.WriteString("Loader\n\n")

		// Register
		b.WriteString("func Register")
		b.WriteString(prefix)
		b.WriteString("(allFiles []string) {\n")
		b.WriteString("\t_, svc := config.SplitFiles(allFiles)\n")
		b.WriteString("\tl := &")
		b.WriteString(prefix)
		b.WriteString("Loader{files: svc}\n")
		b.WriteString("\t")
		b.WriteString(svcName)
		b.WriteString(" = l\n")
		b.WriteString("\tconfig.RegisterService(l)\n")
		b.WriteString("}\n\n")

		// Load methods
		b.WriteString("func (l *")
		b.WriteString(prefix)
		b.WriteString("Loader) Load() error {\n")
		b.WriteString("\tcfg := new(gen.")
		b.WriteString(configType)
		b.WriteString(")\n")
		b.WriteString("\tfor _, f := range l.files {\n")
		b.WriteString("\t\tdata, err := os.ReadFile(f)\n")
		b.WriteString("\t\tif err != nil { return fmt.Errorf(\"read %s: %w\", f, err) }\n")
		b.WriteString("\t\tdata, err = configgen.ExpandUpperEnv(data)\n")
		b.WriteString("\t\tif err != nil { return fmt.Errorf(\"expand env %s: %w\", f, err) }\n")
		b.WriteString("\t\tif err := yaml.Unmarshal(data, cfg); err != nil { return fmt.Errorf(\"parse %s: %w\", f, err) }\n")
		b.WriteString("\t}\n")
		b.WriteString("\tl.shadow = cfg\n")
		b.WriteString("\treturn nil\n")
		b.WriteString("}\n\n")

		b.WriteString("func (l *")
		b.WriteString(prefix)
		b.WriteString("Loader) Check() []string { return l.shadow.CheckStatic(l.cur.Load()) }\n\n")

		b.WriteString("func (l *")
		b.WriteString(prefix)
		b.WriteString("Loader) Validate() []string { return l.shadow.Validate() }\n\n")

		b.WriteString("func (l *")
		b.WriteString(prefix)
		b.WriteString("Loader) Swap() { l.cur.Store(l.shadow); l.shadow = nil }\n\n")

		// typed accessor
		b.WriteString("func ")
		b.WriteString(prefix)
		b.WriteString("Config() *gen.")
		b.WriteString(configType)
		b.WriteString(" {\n")
		b.WriteString("\tif ")
		b.WriteString(svcName)
		b.WriteString(" == nil { return nil }\n")
		b.WriteString("\treturn ")
		b.WriteString(svcName)
		b.WriteString(".cur.Load()\n")
		b.WriteString("}\n")

		path := filepath.Join(dir, svcName+".gen.go")
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Println("generated", path)
		count++
	}
	return count
}

// renderCommonLoader 为 CommonConfig 生成精简 Loader（不热更，不进 ConfigManager）
func renderCommonLoader(msgs []msgInfo) bool {
	for _, msg := range msgs {
		if msg.Name != "CommonConfig" {
			continue
		}
		dir := filepath.Join("conf", "schema", "gen", "loader")
		var b strings.Builder
		b.WriteString("// Code generated by gen_config. DO NOT EDIT.\n")
		b.WriteString("package config\n\n")

		b.WriteString("import (\n")
		b.WriteString("\t\"fmt\"\n\n")
		b.WriteString("\t\"project/conf/schema/gen\"\n")
		b.WriteString("\tconfig \"project/internal/core/config\"\n")
		b.WriteString("\t\"project/pkg/configgen\"\n")
		b.WriteString(")\n\n")

		// Loader struct — 不热更，普通指针
		b.WriteString("type CommonLoader struct {\n")
		b.WriteString("\tfiles []string\n")
		b.WriteString("\tcur   *gen.CommonConfig\n")
		b.WriteString("}\n\n")

		b.WriteString("var common *CommonLoader\n\n")

		// RegisterCommon 筛 common 文件 → 立即加载
		b.WriteString("func RegisterCommon(allFiles []string) {\n")
		b.WriteString("\tcommonFiles, _ := config.SplitFiles(allFiles)\n")
		b.WriteString("\tl := &CommonLoader{files: commonFiles}\n")
		b.WriteString("\tcommon = l\n")
		b.WriteString("\tif err := l.Load(); err != nil { panic(fmt.Sprintf(\"common config load: %v\", err)) }\n")
		b.WriteString("}\n\n")

		// Load — yaml 直通 typed，零 map
		b.WriteString("func (l *CommonLoader) Load() error {\n")
		b.WriteString("\tcfg, err := configgen.LoadFiles[*gen.CommonConfig](l.files...)\n")
		b.WriteString("\tif err != nil { return fmt.Errorf(\"common load: %w\", err) }\n")
		b.WriteString("\tif errs := cfg.Validate(); len(errs) > 0 { return fmt.Errorf(\"common validate: %v\", errs) }\n")
		b.WriteString("\tl.cur = cfg\n")
		b.WriteString("\treturn nil\n")
		b.WriteString("}\n\n")

		// CommonConfig accessor
		b.WriteString("func CommonConfig() *gen.CommonConfig {\n")
		b.WriteString("\tif common == nil { return nil }\n")
		b.WriteString("\treturn common.cur\n")
		b.WriteString("}\n")

		path := filepath.Join(dir, "common.gen.go")
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Println("generated", path)
		return true
	}
	return false
}

// svcNameFromProto 从 proto 文件名提取服务名（"gatesvr.proto" → "gatesvr"）
func svcNameFromProto(fn string) string {
	base := filepath.Base(fn)
	return strings.TrimSuffix(base, ".proto")
}

