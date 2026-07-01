package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
)

type serviceKind string

const (
	kindFrontend serviceKind = "FRONTEND"
	kindBackend  serviceKind = "BACKEND"
)

type serviceSpec struct {
	Name            string
	Kind            serviceKind
	BaseName        string
	FileBaseName    string
	ServerTypeConst string
	Methods         []methodSpec
}

type methodSpec struct {
	Name       string
	Notify     bool
	InputType  string // 简名，如 CS_ClaimReward_Req
	InputPkg   string // import path
	OutputType string // Rsp 类型简名，Notify 时为 ""
	OutputPkg  string
	ShortPkg   string // 本包的简称
	CmdID      string // input message 的 cmd_id，Notify 时必有，Req/Rsp 时为 req 的 cmd_id
	RspCmdID   string // output message 的 cmd_id，Notify 时为 "0"
}

func (m methodSpec) sigTyped() string {
	if m.Notify {
		return fmt.Sprintf("%s(ctx corerpc.Ctx, ntf *%s.%s)", m.Name, m.ShortPkg, m.InputType)
	}
	return fmt.Sprintf("%s(ctx corerpc.Ctx, req *%s.%s, reply corerpc.Reply[*%s.%s])",
		m.Name, m.ShortPkg, m.InputType, m.ShortPkg, m.OutputType)
}

func (m methodSpec) stubSig() string {
	if m.Notify {
		return "func (s *%sStub) " + m.Name + "(ctx corerpc.Ctx, ntf *" + m.ShortPkg + "." + m.InputType + ")"
	}
	return "func (s *%sStub) " + m.Name + "(ctx corerpc.Ctx, req *" + m.ShortPkg + "." + m.InputType + ", cb func(*" + m.ShortPkg + "." + m.OutputType + ", error))"
}

var (
	serviceStartRE = regexp.MustCompile(`(?m)^\s*service\s+(\w+)\s*\{`)
	messageStartRE = regexp.MustCompile(`(?m)^\s*message\s+(\w+)\s*\{`)
	kindRE         = regexp.MustCompile(`option\s*\(protocol\.common\.kind\)\s*=\s*(\w+)\s*;`)
	serverTypeRE   = regexp.MustCompile(`option\s*\(protocol\.common\.server_type\)\s*=\s*(\w+)\s*;`)
	cmdIDRE        = regexp.MustCompile(`option\s*\(protocol\.common\.cmd_id\)\s*=\s*(\d+)\s*;`)
)

func main() {
	protogen.Options{}.Run(run)
}

func run(p *protogen.Plugin) error {
	var backendSpecs []serviceSpec
	for _, f := range p.Files {
		if !f.Generate {
			continue
		}
		specs, err := parseFile(f)
		if err != nil {
			return err
		}
		for _, spec := range specs {
			switch spec.Kind {
			case kindFrontend:
				if err := writeHandlerFile(p, spec); err != nil {
					return err
				}
			case kindBackend:
				if err := writeServiceFile(p, spec); err != nil {
					return err
				}
				backendSpecs = append(backendSpecs, spec)
			default:
				return fmt.Errorf("%s: unknown service kind", spec.Name)
			}
		}
	}
	if len(backendSpecs) > 0 {
		sort.Slice(backendSpecs, func(i, j int) bool { return backendSpecs[i].Name < backendSpecs[j].Name })
		if err := writeRPCFile(p, backendSpecs); err != nil {
			return err
		}
	}
	return nil
}

func parseFile(f *protogen.File) ([]serviceSpec, error) {
	text, err := os.ReadFile(f.Desc.Path())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Desc.Path(), err)
	}
	services, err := extractBlocks(string(text), serviceStartRE)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Desc.Path(), err)
	}

	out := make([]serviceSpec, 0, len(f.Services))
	for _, s := range f.Services {
		body, ok := services[s.GoName]
		if !ok {
			return nil, fmt.Errorf("%s: service %s not found", f.Desc.Path(), s.GoName)
		}
		kind := detectKind(s.GoName, body)
		if err := lintServiceName(s.GoName, kind); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Desc.Path(), err)
		}
		base := strings.TrimSuffix(s.GoName, kindSuffix(kind))
		serverType := firstMatch(serverTypeRE, body)
		if serverType == "" {
			return nil, fmt.Errorf("%s: service %s missing server_type option", f.Desc.Path(), s.GoName)
		}
		methods, err := parseMethods(f, s, kind, string(text))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Desc.Path(), err)
		}
		out = append(out, serviceSpec{
			Name:            s.GoName,
			Kind:            kind,
			BaseName:        base,
			FileBaseName:    toSnake(base),
			ServerTypeConst: serverTypeToConst(serverType),
			Methods:         methods,
		})
	}
	return out, nil
}

func parseMethods(f *protogen.File, s *protogen.Service, kind serviceKind, protoText string) ([]methodSpec, error) {
	// 解析文件中所有 message 块，用于查找 cmd_id
	messages, _ := extractBlocks(protoText, messageStartRE)

	methods := make([]methodSpec, 0, len(s.Methods))
	cmdSeen := make(map[string]bool) // 全局 cmd_id 唯一性检查
	for _, m := range s.Methods {
		notify := isEmptyOutput(m)
		inputName := string(m.Input.GoIdent.GoName)
		inputPkg := string(m.Input.GoIdent.GoImportPath)
		if inputPkg == "" {
			inputPkg = string(f.GoImportPath)
		}
		shortPkg := lastSeg(inputPkg)

		var outputName, outputPkg string
		if !notify {
			outputName = string(m.Output.GoIdent.GoName)
			outputPkg = string(m.Output.GoIdent.GoImportPath)
			if outputPkg == "" {
				outputPkg = string(f.GoImportPath)
			}
		}
		// lint 命名规则
		if err := lintMethodName(m.GoName, inputName, outputName, notify, kind); err != nil {
			return nil, err
		}

		// 从 message 块提取 cmd_id
		inputBody := messages[inputName]
		cmdID := firstMatch(cmdIDRE, inputBody)
		if cmdID == "" {
			cmdID = "0" // 无 cmd_id 时默认为 0（Service 类无 cmd_id，FRONTEND 必须有）
		}
		if cmdID == "0" && kind == kindFrontend {
			return nil, fmt.Errorf("method %s: input %s missing or zero cmd_id (required for FRONTEND)", m.GoName, inputName)
		}
		if cmdSeen[cmdID] && cmdID != "0" {
			return nil, fmt.Errorf("method %s: cmd_id %s is duplicated", m.GoName, cmdID)
		}
		if cmdID != "0" {
			cmdSeen[cmdID] = true
		}

		rspCmdID := "0"
		if !notify {
			outputBody := messages[outputName]
			rspID := firstMatch(cmdIDRE, outputBody)
			if rspID != "" {
				rspCmdID = rspID
			}
		}

		methods = append(methods, methodSpec{
			Name:       m.GoName,
			Notify:     notify,
			InputType:  inputName,
			InputPkg:   inputPkg,
			OutputType: outputName,
			OutputPkg:  outputPkg,
			ShortPkg:   shortPkg,
			CmdID:      cmdID,
			RspCmdID:   rspCmdID,
		})
	}
	return methods, nil
}

func lintMethodName(methodName, inputName, outputName string, notify bool, kind serviceKind) error {
	if notify {
		if !strings.HasSuffix(inputName, "_Ntf") {
			return fmt.Errorf("method %s: returns Empty but input %s not ending with _Ntf", methodName, inputName)
		}
	} else {
		if !strings.HasSuffix(inputName, "_Req") {
			return fmt.Errorf("method %s: returns value but input %s not ending with _Req", methodName, inputName)
		}
		if !strings.HasSuffix(outputName, "_Rsp") {
			return fmt.Errorf("method %s: returns value but output %s not ending with _Rsp", methodName, outputName)
		}
	}
	if kind == kindFrontend && strings.HasSuffix(inputName, "_Rsp") {
		return fmt.Errorf("method %s: FRONTEND input %s must not end with _Rsp", methodName, inputName)
	}
	return nil
}

func isEmptyOutput(m *protogen.Method) bool {
	if m.Output == nil || m.Output.Desc == nil {
		return false
	}
	return string(m.Output.Desc.FullName()) == "google.protobuf.Empty"
}

func detectKind(serviceName, body string) serviceKind {
	if m := firstMatch(kindRE, body); m != "" {
		switch m {
		case string(kindFrontend):
			return kindFrontend
		case string(kindBackend):
			return kindBackend
		}
	}
	if strings.HasSuffix(serviceName, kindSuffix(kindFrontend)) {
		return kindFrontend
	}
	return kindBackend
}

func lintServiceName(name string, kind serviceKind) error {
	if !strings.HasSuffix(name, kindSuffix(kind)) {
		return fmt.Errorf("%s service %s must end with %s", kind, name, kindSuffix(kind))
	}
	return nil
}

func kindSuffix(kind serviceKind) string {
	if kind == kindFrontend {
		return "Handler"
	}
	return "Service"
}

func writeHandlerFile(p *protogen.Plugin, spec serviceSpec) error {
	g := p.NewGeneratedFile(filepath.Join("handler", spec.FileBaseName+"_handler.go"), "project/protocol/gen/handler")
	g.P("// Code generated by protoc-gen-svcstub. DO NOT EDIT.")
	g.P("package handler")
	g.P("")
	imports := typeImports(spec)
	imports["project/internal/core/dispatcher"] = true
	imports["project/internal/core/codec"] = true
	imports["project/internal/core/errcode"] = true
	imports["project/internal/core/session"] = true
	imports["project/internal/core/rpc"] = true
	imports["google.golang.org/protobuf/proto"] = true
	sortAndWriteImports(g, imports, map[string]string{
		"project/internal/core/rpc": "corerpc",
	})
	g.P("")
	g.P("type ", spec.Name, " interface {")
	for _, m := range spec.Methods {
		g.P("\t", m.sigTyped())
	}
	g.P("}")
	g.P("")
	// Register 函数：为每个方法调用 d.RegisterHandler，包裹 unmarshal adapter
	g.P("func Register", spec.Name, "(d *dispatcher.Dispatcher, srv ", spec.Name, ") {")
	for _, m := range spec.Methods {
		if m.Notify {
			g.P("\t// ", spec.Name, "/", m.Name, " (Notify) cmd=", m.CmdID)
			g.P("\td.RegisterHandler(", m.CmdID, ", func(sess *session.Session, msg *codec.Message) error {")
			g.P("\t\tvar ntf ", m.ShortPkg, ".", m.InputType)
			g.P("\t\tif err := proto.Unmarshal(msg.Body, &ntf); err != nil {")
			g.P("\t\t\treturn errcode.New(errcode.ERR_UNMARSHAL, err.Error())")
			g.P("\t\t}")
			g.P("\t\tctx := corerpc.Background()")
			g.P("\t\tsrv.", m.Name, "(ctx, &ntf)")
			g.P("\t\treturn nil")
			g.P("\t})")
		} else {
			g.P("\t// ", spec.Name, "/", m.Name, " (Req/Rsp) cmd=", m.CmdID, " rsp=", m.RspCmdID)
			g.P("\td.RegisterHandler(", m.CmdID, ", func(sess *session.Session, msg *codec.Message) error {")
			g.P("\t\tvar req ", m.ShortPkg, ".", m.InputType)
			g.P("\t\tif err := proto.Unmarshal(msg.Body, &req); err != nil {")
			g.P("\t\t\treturn errcode.New(errcode.ERR_UNMARSHAL, err.Error())")
			g.P("\t\t}")
			g.P("\t\tctx := corerpc.Background()")
			g.P("\t\tsrv.", m.Name, "(ctx, &req, func(rsp *", m.ShortPkg, ".", m.OutputType, ", err error) {")
			g.P("\t\t\tif err != nil || rsp == nil {")
			g.P("\t\t\t\treturn")
			g.P("\t\t\t}")
			g.P("\t\t\tdata, merr := proto.Marshal(rsp)")
			g.P("\t\t\tif merr != nil {")
			g.P("\t\t\t\treturn")
			g.P("\t\t\t}")
			g.P("\t\t\t// reply writes back via ragent; path goes through rpc.Core")
			g.P("\t\t\t_ = sess")
			g.P("\t\t\t_ = data")
			g.P("\t\t})")
			g.P("\t\treturn nil")
			g.P("\t})")
		}
	}
	g.P("}")
	return nil
}

func writeServiceFile(p *protogen.Plugin, spec serviceSpec) error {
	g := p.NewGeneratedFile(filepath.Join("service", spec.FileBaseName+"_service.go"), "project/protocol/gen/service")
	g.P("// Code generated by protoc-gen-svcstub. DO NOT EDIT.")
	g.P("package service")
	g.P("")
	imports := typeImports(spec)
	imports["project/internal/core/dispatcher"] = true
	imports["project/internal/core/rpc"] = true
	sortAndWriteImports(g, imports, map[string]string{
		"project/internal/core/rpc": "corerpc",
	})
	g.P("")
	g.P("type ", spec.Name, " interface {")
	for _, m := range spec.Methods {
		g.P("\t", m.sigTyped())
	}
	g.P("}")
	g.P("")
	g.P("func Register", spec.Name, "(d *dispatcher.Dispatcher, srv ", spec.Name, ") {")
	for _, m := range spec.Methods {
		route := spec.Name + "/" + m.Name
		g.P("\t// ", route)
		g.P("\t_ = d")
		g.P("\t_ = srv")
	}
	g.P("}")
	return nil
}

func writeRPCFile(p *protogen.Plugin, specs []serviceSpec) error {
	g := p.NewGeneratedFile("rpc.go", "project/protocol/gen")
	g.P("// Code generated by protoc-gen-svcstub. DO NOT EDIT.")
	g.P("package rpc")
	g.P("")
	// 收集所有类型 import
	allImports := make(map[string]bool)
	allImports["time"] = true
	allImports["google.golang.org/protobuf/proto"] = true
	allImports["project/internal/core/errcode"] = true
	allImports["project/internal/core/rpc"] = true
	for _, spec := range specs {
		for _, m := range spec.Methods {
			if m.InputPkg != "" && m.InputPkg != "project/internal/core/rpc" && m.InputPkg != "project/internal/core/errcode" {
				allImports[m.InputPkg] = true
			}
			if m.OutputPkg != "" && m.OutputPkg != "project/internal/core/rpc" && m.OutputPkg != "project/internal/core/errcode" {
				allImports[m.OutputPkg] = true
			}
		}
	}
	sortAndWriteImports(g, allImports, map[string]string{
		"project/internal/core/rpc": "corerpc",
	})
	g.P("")
	g.P("const (")
	g.P("\tserverTypeGate        = 1")
	g.P("\tserverTypeLobby       = 2")
	g.P("\tserverTypeRoom        = 3")
	g.P("\tserverTypeMatch       = 4")
	g.P("\tserverTypeOnline      = 5")
	g.P("\tserverTypeRouterAgent = 6")
	g.P(")")
	g.P("")
	g.P("type Stub struct {")
	g.P("\tcore   *corerpc.Core")
	g.P("\ttarget corerpc.Target")
	g.P("}")
	g.P("")
	g.P("func NewStub(core *corerpc.Core, serverType uint32) *Stub {")
	g.P("\treturn &Stub{core: core, target: corerpc.Target{ServerType: serverType}}")
	g.P("}")
	g.P("")
	g.P("func (s *Stub) At(nodeID uint32) *Stub {")
	g.P("\tcp := *s")
	g.P("\tcp.target = cp.target.At(nodeID)")
	g.P("\treturn &cp")
	g.P("}")
	g.P("")
	g.P("func (s *Stub) ByHash(key string) *Stub {")
	g.P("\tcp := *s")
	g.P("\tcp.target = cp.target.ByHash(key)")
	g.P("\treturn &cp")
	g.P("}")
	g.P("")
	g.P("func (s *Stub) Broadcast() *Stub {")
	g.P("\tcp := *s")
	g.P("\tcp.target = cp.target.Broadcast()")
	g.P("\treturn &cp")
	g.P("}")
	g.P("")
	g.P("func (s *Stub) Timeout(d time.Duration) *Stub {")
	g.P("\tcp := *s")
	g.P("\tcp.target = cp.target.Timeout(d)")
	g.P("\treturn &cp")
	g.P("}")
	g.P("")
	// typedCall: marshal -> core.Call -> unmarshal -> cb
	g.P("func (s *Stub) typedCall(ctx corerpc.Ctx, route string, req proto.Message, cb func([]byte, error)) {")
	g.P("\tif s == nil || s.core == nil {")
	g.P("\t\tif cb != nil {")
	g.P("\t\t\tcb(nil, errcode.New(errcode.ERR_INTERNAL, \"rpc stub not initialized\"))")
	g.P("\t\t}")
	g.P("\t\treturn")
	g.P("\t}")
	g.P("\tbody, err := proto.Marshal(req)")
	g.P("\tif err != nil {")
	g.P("\t\tif cb != nil {")
	g.P("\t\t\tcb(nil, err)")
	g.P("\t\t}")
	g.P("\t\treturn")
	g.P("\t}")
	g.P("\ts.core.Call(s.target, route, body, ctx, func(payload []byte, code errcode.ErrCode) {")
	g.P("\t\tif ctx.Stale() {")
	g.P("\t\t\treturn")
	g.P("\t\t}")
	g.P("\t\tif cb == nil {")
	g.P("\t\t\treturn")
	g.P("\t\t}")
	g.P("\t\tif code != errcode.OK {")
	g.P("\t\t\tcb(nil, errcode.From(code))")
	g.P("\t\t\treturn")
	g.P("\t\t}")
	g.P("\t\tcb(payload, nil)")
	g.P("\t})")
	g.P("}")
	g.P("")
	// typedSend: marshal -> core.Send
	g.P("func (s *Stub) typedSend(ctx corerpc.Ctx, route string, ntf proto.Message) {")
	g.P("\tif s == nil || s.core == nil {")
	g.P("\t\treturn")
	g.P("\t}")
	g.P("\tbody, err := proto.Marshal(ntf)")
	g.P("\tif err != nil {")
	g.P("\t\treturn")
	g.P("\t}")
	g.P("\ts.core.Send(s.target, route, body, ctx)")
	g.P("}")
	g.P("")
	// 每个 backend spec 生成 typed stub
	for _, spec := range specs {
		stubName := spec.BaseName + "Stub"
		g.P("type ", stubName, " struct{ *Stub }")
		g.P("")
		for _, m := range spec.Methods {
			route := spec.Name + "/" + m.Name
			if m.Notify {
				g.P("func (s *", stubName, ") ", m.Name, "(ctx corerpc.Ctx, ntf *", m.ShortPkg, ".", m.InputType, ") {")
				g.P("\ts.typedSend(ctx, \"", route, "\", ntf)")
				g.P("}")
			} else {
				g.P("func (s *", stubName, ") ", m.Name, "(ctx corerpc.Ctx, req *", m.ShortPkg, ".", m.InputType, ", cb func(*", m.ShortPkg, ".", m.OutputType, ", error)) {")
				g.P("\ts.typedCall(ctx, \"", route, "\", req, func(body []byte, err error) {")
				g.P("\t\tif err != nil {")
				g.P("\t\t\tcb(nil, err)")
				g.P("\t\t\treturn")
				g.P("\t\t}")
				g.P("\t\tvar rsp ", m.ShortPkg, ".", m.OutputType)
				g.P("\t\tif uerr := proto.Unmarshal(body, &rsp); uerr != nil {")
				g.P("\t\t\tcb(nil, uerr)")
				g.P("\t\t\treturn")
				g.P("\t\t}")
				g.P("\t\tcb(&rsp, nil)")
				g.P("\t})")
				g.P("}")
			}
			g.P("")
		}
	}
	g.P("var (")
	for _, spec := range specs {
		g.P("\t", spec.BaseName, " = &", spec.BaseName, "Stub{Stub: NewStub(nil, ", spec.ServerTypeConst, ")}")
	}
	g.P(")")
	g.P("")
	g.P("func Init(core *corerpc.Core) {")
	for _, spec := range specs {
		g.P("\t", spec.BaseName, ".Stub.core = core")
	}
	g.P("}")
	g.P("")
	g.P("type Next = corerpc.Next")
	g.P("")
	g.P("type Sequence = corerpc.Sequence")
	g.P("")
	g.P("func Join2[A, B any](ctx corerpc.Ctx, a func(func(*A, error)), b func(func(*B, error)), done func(*A, *B, error)) {")
	g.P("\tcorerpc.Join2(ctx, a, b, done)")
	g.P("}")
	g.P("")
	g.P("func Join3[A, B, C any](ctx corerpc.Ctx, a func(func(*A, error)), b func(func(*B, error)), c func(func(*C, error)), done func(*A, *B, *C, error)) {")
	g.P("\tcorerpc.Join3(ctx, a, b, c, done)")
	g.P("}")
	g.P("")
	g.P("func Join4[A, B, C, D any](ctx corerpc.Ctx, a func(func(*A, error)), b func(func(*B, error)), c func(func(*C, error)), d func(func(*D, error)), done func(*A, *B, *C, *D, error)) {")
	g.P("\tcorerpc.Join4(ctx, a, b, c, d, done)")
	g.P("}")
	g.P("")
	g.P("func Each[T, R any](ctx corerpc.Ctx, items []T, step func(T, func(*R, error)), done func([]*R, error)) {")
	g.P("\tcorerpc.Each(ctx, items, step, done)")
	g.P("}")
	g.P("")
	g.P("func Seq(ctx corerpc.Ctx) *Sequence { return corerpc.Seq(ctx) }")
	return nil
}

func typeImports(spec serviceSpec) map[string]bool {
	m := make(map[string]bool)
	for _, method := range spec.Methods {
		if method.InputPkg != "" && method.InputPkg != "project/internal/core/rpc" {
			m[method.InputPkg] = true
		}
		if method.OutputPkg != "" && method.OutputPkg != "project/internal/core/rpc" {
			m[method.OutputPkg] = true
		}
	}
	return m
}

func sortAndWriteImports(g *protogen.GeneratedFile, imports map[string]bool, forcedAliases map[string]string) {
	if len(imports) == 0 {
		return
	}
	// 分组：标准库、第三方、项目内
	var std, third, project []string
	for p := range imports {
		if strings.Contains(p, ".") && !strings.HasPrefix(p, "project/") {
			third = append(third, p)
		} else if strings.HasPrefix(p, "project/") {
			project = append(project, p)
		} else {
			std = append(std, p)
		}
	}
	sort.Strings(std)
	sort.Strings(third)
	sort.Strings(project)

	g.P("import (")
	writeGroup(g, std, forcedAliases)
	if len(third) > 0 && (len(std) > 0 || len(project) > 0) {
		g.P("")
	}
	writeGroup(g, third, forcedAliases)
	if len(project) > 0 && (len(std) > 0 || len(third) > 0) {
		g.P("")
	}
	writeGroup(g, project, forcedAliases)
	g.P(")")
}

func writeGroup(g *protogen.GeneratedFile, paths []string, forcedAliases map[string]string) {
	for _, p := range paths {
		alias := lastSeg(p)
		if a, ok := forcedAliases[p]; ok {
			alias = a
		}
		g.P("\t", alias, " \"", p, "\"")
	}
}

func extractBlocks(text string, startRE *regexp.Regexp) (map[string]string, error) {
	matches := startRE.FindAllStringSubmatchIndex(text, -1)
	out := make(map[string]string, len(matches))
	for _, m := range matches {
		if len(m) < 4 {
			continue
		}
		name := text[m[2]:m[3]]
		open := m[1] - 1
		if open < 0 || open >= len(text) || text[open] != '{' {
			return nil, fmt.Errorf("block %s missing opening brace", name)
		}
		close, err := matchBrace(text, open)
		if err != nil {
			return nil, fmt.Errorf("block %s: %w", name, err)
		}
		out[name] = text[open+1 : close]
	}
	return out, nil
}

func matchBrace(text string, open int) (int, error) {
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("unmatched brace")
}

func firstMatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func serverTypeToConst(name string) string {
	switch strings.TrimSpace(name) {
	case "ST_GATESVR":
		return "serverTypeGate"
	case "ST_LOBBYSVR":
		return "serverTypeLobby"
	case "ST_ROOMSVR":
		return "serverTypeRoom"
	case "ST_MATCHSVR":
		return "serverTypeMatch"
	case "ST_ONLINESVR":
		return "serverTypeOnline"
	case "ST_ROUTERAGENT":
		return "serverTypeRouterAgent"
	default:
		return name
	}
}

func lastSeg(path string) string {
	idx := strings.LastIndexByte(path, '/')
	if idx >= 0 {
		return path[idx+1:]
	}
	return path
}

func toSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && isUpper(r) {
			prev := rune(s[i-1])
			if isLower(prev) {
				// 小写→大写边界：fooBar → foo_bar
				b.WriteByte('_')
			} else if isUpper(prev) && i+1 < len(s) && isLower(rune(s[i+1])) {
				// 大写缩写→小写边界：BARBaz → bar_baz
				b.WriteByte('_')
			}
		}
		b.WriteRune(toLower(r))
	}
	return b.String()
}

func isUpper(r rune) bool { return r >= 'A' && r <= 'Z' }
func isLower(r rune) bool { return r >= 'a' && r <= 'z' }
func toLower(r rune) rune {
	if isUpper(r) {
		return r + ('a' - 'A')
	}
	return r
}
