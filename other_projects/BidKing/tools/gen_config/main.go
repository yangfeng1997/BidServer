// gen_config 读取 protoc 输出的 FileDescriptorSet 二进制（.pb），用 protoreflect
// 遍历配置 message，生成带 yaml: tag 的 Go struct（config.go）+ 三张字段表
// （reload_table.go：ReloadableFields / EnvFields / RequiredFields）。
//
// 设计要点：
//   - 不依赖 protoc-gen-go 产物（gen_config 正是要生成 config 包，不能导入它）。
//     option 用原始 extension number（reload=50101 / env=50102 / required=50103），
//     从 FieldOptions 的 wire bytes 按 field number 解析 bool。
//   - 特性白名单：仅允许标量（string/int32/int64/uint32/uint64/bool/float/double）
//   - 嵌套 message + repeated。出现 enum/oneof/map/group/bytes 报错并指出位置。
//   - env 字段必须是 string，否则报错。
//   - 三张表均为 map[string]map[string]bool（外层 key=message 名，内层 key=点路径，
//     如 "common.redis.password"），递归嵌套 message。
//   - 生成时把每个 message 的静态字段（非 reload）打印到 stderr，供作者自查。
//
// 用法：go run ./tools/gen_config --pb=conf/schema/gen/config.pb --out=conf/schema/gen
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// option extension 的 field number（与 config_options.proto 一致）。
const (
	extReload   = 50101
	extEnv      = 50102
	extRequired = 50103
)

func main() {
	pbPath := flag.String("pb", "conf/schema/gen/config.pb.descriptor", "protoc 输出的 FileDescriptorSet 二进制路径")
	outDir := flag.String("out", "conf/schema/gen", "生成文件输出目录")
	flag.Parse()

	if err := generate(*pbPath, *outDir); err != nil {
		fmt.Fprintf(os.Stderr, "gen_config 失败: %v\n", err)
		os.Exit(1)
	}
}

// runProtoc 调用 protoc（供测试构造 descriptor 用）。
func runProtoc(args []string) error {
	cmd := exec.Command("protoc", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("protoc %v: %v\n%s", args, err, out)
	}
	return nil
}

// generate 读 .pb → 解析 FileDescriptorSet → protoreflect 遍历配置 message →
// 生成 config.go 与 reload_table.go。
func generate(pbPath, outDir string) error {
	raw, err := os.ReadFile(pbPath)
	if err != nil {
		return fmt.Errorf("读 descriptor %s: %w", pbPath, err)
	}

	var fdSet descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(raw, &fdSet); err != nil {
		return fmt.Errorf("解析 FileDescriptorSet: %w", err)
	}

	files, err := protodesc.NewFiles(&fdSet)
	if err != nil {
		return fmt.Errorf("构建 FileRegistry: %w", err)
	}

	// 收集配置 message（跳过 google/ 前缀文件与 config_options.proto；后者只承载 option 扩展定义）。
	var messages []protoreflect.MessageDescriptor
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		p := fd.Path()
		if strings.HasPrefix(p, "google/") {
			return true
		}
		if path.Base(p) == "options.proto" {
			return true
		}
		mds := fd.Messages()
		for i := 0; i < mds.Len(); i++ {
			messages = append(messages, mds.Get(i))
		}
		return true
	})

	// 输出稳定：按 message 名排序。
	sort.Slice(messages, func(i, j int) bool {
		return string(messages[i].Name()) < string(messages[j].Name())
	})

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录 %s: %w", outDir, err)
	}

	structSrc, err := genStructFile(messages)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path.Join(outDir, "config.go"), structSrc, 0o644); err != nil {
		return fmt.Errorf("写 config.go: %w", err)
	}

	tableSrc, err := genTableFile(messages)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path.Join(outDir, "reload_table.go"), tableSrc, 0o644); err != nil {
		return fmt.Errorf("写 reload_table.go: %w", err)
	}

	printStaticSummary(messages)
	return nil
}

// getBoolExtension 从 FieldOptions 序列化后的 wire bytes 中，按 field number 解析 bool 扩展。
// 留空与 = false 都返回 false，= true 返回 true。
func getBoolExtension(opts *descriptorpb.FieldOptions, fieldNum int32) (bool, error) {
	if opts == nil {
		return false, nil
	}
	b, err := proto.Marshal(opts)
	if err != nil {
		return false, fmt.Errorf("序列化 FieldOptions: %w", err)
	}
	for len(b) > 0 {
		num, typ, n := consumeTag(b)
		if n <= 0 {
			return false, fmt.Errorf("FieldOptions wire 解析失败")
		}
		b = b[n:]
		if num == fieldNum && typ == 0 { // varint，bool
			v, m := consumeVarint(b)
			if m <= 0 {
				return false, fmt.Errorf("FieldOptions varint 解析失败")
			}
			return v != 0, nil
		}
		// 非目标 field：跳过该值。
		skip, err := skipValue(b, typ)
		if err != nil {
			return false, err
		}
		b = b[skip:]
	}
	return false, nil
}

// consumeTag 解析一个 wire tag，返回 field number、wire type、消耗字节数。
func consumeTag(b []byte) (num int32, typ int, n int) {
	v, m := consumeVarint(b)
	if m <= 0 {
		return 0, 0, 0
	}
	return int32(v >> 3), int(v & 0x7), m
}

// consumeVarint 解析一个 varint，返回值与消耗字节数（失败返回 0,0）。
func consumeVarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b); i++ {
		v |= uint64(b[i]&0x7f) << (7 * uint(i))
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
		if i >= 9 {
			return 0, 0
		}
	}
	return 0, 0
}

// skipValue 跳过给定 wire type 的一个值，返回消耗字节数。
func skipValue(b []byte, typ int) (int, error) {
	switch typ {
	case 0: // varint
		_, m := consumeVarint(b)
		if m <= 0 {
			return 0, fmt.Errorf("跳过 varint 失败")
		}
		return m, nil
	case 1: // 64-bit
		if len(b) < 8 {
			return 0, fmt.Errorf("跳过 64-bit 失败")
		}
		return 8, nil
	case 2: // length-delimited
		l, m := consumeVarint(b)
		if m <= 0 {
			return 0, fmt.Errorf("跳过 length-delimited 长度失败")
		}
		if len(b) < m+int(l) {
			return 0, fmt.Errorf("跳过 length-delimited 内容越界")
		}
		return m + int(l), nil
	case 5: // 32-bit
		if len(b) < 4 {
			return 0, fmt.Errorf("跳过 32-bit 失败")
		}
		return 4, nil
	default:
		return 0, fmt.Errorf("不支持的 wire type %d", typ)
	}
}

// kindAllowed 检查字段类型是否在白名单内（标量 + 嵌套 message）。
// repeated 由调用方在外层处理；map/group/oneof/enum/bytes 一律拒绝。
func kindAllowed(fd protoreflect.FieldDescriptor) bool {
	switch fd.Kind() {
	case protoreflect.StringKind,
		protoreflect.Int32Kind, protoreflect.Int64Kind,
		protoreflect.Uint32Kind, protoreflect.Uint64Kind,
		protoreflect.BoolKind,
		protoreflect.FloatKind, protoreflect.DoubleKind:
		return true
	case protoreflect.MessageKind:
		// map 在 protoreflect 里也是 MessageKind（IsMap），需排除。
		return !fd.IsMap()
	default:
		// EnumKind / BytesKind / GroupKind / Sint*/Fixed* 等一律不在白名单。
		return false
	}
}

// scalarGoType 返回标量字段对应的 Go 类型名。
func scalarGoType(fd protoreflect.FieldDescriptor) string {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return "string"
	case protoreflect.Int32Kind:
		return "int32"
	case protoreflect.Int64Kind:
		return "int64"
	case protoreflect.Uint32Kind:
		return "uint32"
	case protoreflect.Uint64Kind:
		return "uint64"
	case protoreflect.BoolKind:
		return "bool"
	case protoreflect.FloatKind:
		return "float32"
	case protoreflect.DoubleKind:
		return "float64"
	default:
		return ""
	}
}

// goType 返回字段对应的 Go 类型（含 repeated 的 [] 与嵌套 message 的 *TypeName）。
func goType(fd protoreflect.FieldDescriptor) string {
	var base string
	if fd.Kind() == protoreflect.MessageKind {
		base = "*" + string(fd.Message().Name())
	} else {
		base = scalarGoType(fd)
	}
	if fd.Cardinality() == protoreflect.Repeated {
		// repeated message 用 []*TypeName；repeated 标量用 []T。
		return "[]" + base
	}
	return base
}

// toCamel 把 snake_case 转 CamelCase（首字母大写，导出字段）。
func toCamel(s string) string {
	parts := strings.Split(s, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

// genStructFile 为每个 message 生成带 yaml: tag 的 struct。
// 遇白名单外类型或 env 非 string 报错，最后用 go/format 格式化。
func genStructFile(messages []protoreflect.MessageDescriptor) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("// Code generated by gen_config. DO NOT EDIT.\n")
	buf.WriteString("// 由 tools/gen_config 从 conf/schema/*.proto 的 descriptor 生成。\n\n")
	buf.WriteString("package conf\n\n")

	for _, md := range messages {
		buf.WriteString(fmt.Sprintf("// %s 由 proto message %s 生成。\n", md.Name(), md.FullName()))
		buf.WriteString(fmt.Sprintf("type %s struct {\n", md.Name()))
		fields := md.Fields()
		for i := 0; i < fields.Len(); i++ {
			fd := fields.Get(i)
			if fd.ContainingOneof() != nil {
				return nil, fmt.Errorf("不支持的特性 oneof：%s 字段 %s", md.FullName(), fd.Name())
			}
			if !kindAllowed(fd) {
				return nil, fmt.Errorf("不支持的字段类型 %s（白名单外，疑似 enum/map/bytes/group）：%s 字段 %s",
					fd.Kind(), md.FullName(), fd.Name())
			}
			// env 字段必须是 string。
			opts, _ := fd.Options().(*descriptorpb.FieldOptions)
			isEnv, err := getBoolExtension(opts, extEnv)
			if err != nil {
				return nil, fmt.Errorf("%s 字段 %s 读 env option: %w", md.FullName(), fd.Name(), err)
			}
			if isEnv && fd.Kind() != protoreflect.StringKind {
				return nil, fmt.Errorf("env 字段必须是 string 类型：%s 字段 %s（实际 %s）",
					md.FullName(), fd.Name(), fd.Kind())
			}
			name := string(fd.Name())
			buf.WriteString(fmt.Sprintf("\t%s %s `yaml:%q`\n", toCamel(name), goType(fd), name))
		}
		buf.WriteString("}\n\n")
	}

	src, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("格式化 config.go: %w\n源码:\n%s", err, buf.String())
	}
	return src, nil
}

// collectPaths 递归收集满足 filter 的字段点路径到 out。
// prefix 是当前 message 在顶层 message 内的点路径前缀（顶层为空）。
func collectPaths(md protoreflect.MessageDescriptor, prefix string, filter func(protoreflect.FieldDescriptor) (bool, error), out map[string]bool) error {
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		name := string(fd.Name())
		full := name
		if prefix != "" {
			full = prefix + "." + name
		}
		ok, err := filter(fd)
		if err != nil {
			return err
		}
		if ok {
			out[full] = true
		}
		// 递归进入嵌套 message（非 repeated；repeated message 的内部路径无单一点路径语义，跳过）。
		if fd.Kind() == protoreflect.MessageKind && !fd.IsMap() && fd.Cardinality() != protoreflect.Repeated {
			if err := collectPaths(fd.Message(), full, filter, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// boolExtFilter 返回一个按指定 extension number 取 bool 的 filter。
func boolExtFilter(fieldNum int32) func(protoreflect.FieldDescriptor) (bool, error) {
	return func(fd protoreflect.FieldDescriptor) (bool, error) {
		opts, _ := fd.Options().(*descriptorpb.FieldOptions)
		return getBoolExtension(opts, fieldNum)
	}
}

// genTableFile 生成 ReloadableFields / EnvFields / RequiredFields 三张表。
func genTableFile(messages []protoreflect.MessageDescriptor) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("// Code generated by gen_config. DO NOT EDIT.\n")
	buf.WriteString("// 由 tools/gen_config 从 conf/schema/*.proto 的 descriptor 生成。\n\n")
	buf.WriteString("package conf\n\n")

	type table struct {
		varName string
		comment string
		ext     int32
	}
	tables := []table{
		{"ReloadableFields", "ReloadableFields 配置 message 名 → 可热更字段路径集（标 reload=true）。", extReload},
		{"EnvFields", "EnvFields 运行时 ${VAR} 注入字段路径集（标 env=true）。", extEnv},
		{"RequiredFields", "RequiredFields 必填字段路径集（标 required=true）。", extRequired},
	}

	for _, tb := range tables {
		buf.WriteString(fmt.Sprintf("// %s\n", tb.comment))
		buf.WriteString(fmt.Sprintf("var %s = map[string]map[string]bool{\n", tb.varName))
		for _, md := range messages {
			paths := map[string]bool{}
			if err := collectPaths(md, "", boolExtFilter(tb.ext), paths); err != nil {
				return nil, fmt.Errorf("收集 %s 的 %s 路径: %w", md.Name(), tb.varName, err)
			}
			if len(paths) == 0 {
				continue
			}
			sorted := make([]string, 0, len(paths))
			for p := range paths {
				sorted = append(sorted, p)
			}
			sort.Strings(sorted)
			buf.WriteString(fmt.Sprintf("\t%q: {\n", string(md.Name())))
			for _, p := range sorted {
				buf.WriteString(fmt.Sprintf("\t\t%q: true,\n", p))
			}
			buf.WriteString("\t},\n")
		}
		buf.WriteString("}\n\n")
	}

	src, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("格式化 reload_table.go: %w\n源码:\n%s", err, buf.String())
	}
	return src, nil
}

// printStaticSummary 把每个 message 的静态字段（非 reload）打印到 stderr，供作者自查。
func printStaticSummary(messages []protoreflect.MessageDescriptor) {
	fmt.Fprintln(os.Stderr, "=== gen_config 静态字段自查清单（非 reload 字段，确认是否有本想热更却漏标的）===")
	for _, md := range messages {
		static := map[string]bool{}
		_ = collectPaths(md, "", func(fd protoreflect.FieldDescriptor) (bool, error) {
			// 只收集标量与 repeated 标量这类「叶子」字段；嵌套 message 本身不算叶子。
			if fd.Kind() == protoreflect.MessageKind && fd.Cardinality() != protoreflect.Repeated {
				return false, nil
			}
			opts, _ := fd.Options().(*descriptorpb.FieldOptions)
			isReload, _ := getBoolExtension(opts, extReload)
			return !isReload, nil
		}, static)
		if len(static) == 0 {
			continue
		}
		sorted := make([]string, 0, len(static))
		for p := range static {
			sorted = append(sorted, p)
		}
		sort.Strings(sorted)
		fmt.Fprintf(os.Stderr, "[%s] 静态: %s\n", md.Name(), strings.Join(sorted, ", "))
	}
}
