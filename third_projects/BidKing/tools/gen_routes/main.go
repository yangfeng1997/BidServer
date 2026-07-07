package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type routeItem struct {
	CmdID      string
	ServerType string
	Route      string
	RspCmdID   string
	NoAuth     bool
}

var (
	serviceStartRE = regexp.MustCompile(`(?m)^\s*service\s+(\w+)\s*\{`)
	messageStartRE = regexp.MustCompile(`(?m)^\s*message\s+(\w+)\s*\{`)
	rpcRE          = regexp.MustCompile(`rpc\s+(\w+)\s*\(([\w.]+)\)\s*returns\s*\(([\w.]+)\)`)
	cmdIDRE        = regexp.MustCompile(`option\s*\(protocol\.common\.cmd_id\)\s*=\s*(\d+)\s*;`)
	noAuthRE       = regexp.MustCompile(`option\s*\(protocol\.common\.no_auth\)\s*=\s*true\s*;`)
	kindRE         = regexp.MustCompile(`option\s*\(protocol\.common\.kind\)\s*=\s*(\w+)\s*;`)
	serverTypeRE   = regexp.MustCompile(`option\s*\(protocol\.common\.server_type\)\s*=\s*(\w+)\s*;`)
)

func main() {
	var protoDir string
	var out string
	flag.StringVar(&protoDir, "proto", "protocol/handler", "proto directory")
	flag.StringVar(&out, "out", "protocol/gen/routes.go", "output file")
	flag.Parse()

	routes, err := scanRoutes(protoDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, []byte(renderRoutes(routes)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func scanRoutes(dir string) ([]routeItem, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.proto"))
	if err != nil {
		return nil, err
	}
	var items []routeItem
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		text := string(b)
		messages, err := extractBlocks(text, messageStartRE)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		services, err := extractBlocks(text, serviceStartRE)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for name, body := range services {
			if firstMatch(kindRE, body) != "FRONTEND" {
				continue
			}
			serverType := firstMatch(serverTypeRE, body)
			if serverType == "" {
				continue
			}
			for _, rpcMatch := range rpcRE.FindAllStringSubmatch(body, -1) {
				rpcName := rpcMatch[1]
				input := rpcMatch[2]
				output := rpcMatch[3]
				inputMsg, ok := messages[input]
				if !ok {
					continue
				}
				cmdID := firstMatch(cmdIDRE, inputMsg)
				if cmdID == "" {
					continue
				}
				// cmd_id=0 保留，不允许使用
				if cmdID == "0" {
					return nil, fmt.Errorf("%s: message %s has cmd_id=0 which is reserved", path, input)
				}
				rspCmdID := "0"
				if output != "google.protobuf.Empty" {
					if outputMsg, ok := messages[output]; ok {
						rspCmdID = firstMatch(cmdIDRE, outputMsg)
						if rspCmdID == "" {
							rspCmdID = "0"
						}
					}
				}
				items = append(items, routeItem{
					CmdID:      cmdID,
					ServerType: serverTypeToConst(serverType),
					Route:      name + "/" + rpcName,
					RspCmdID:   rspCmdID,
					NoAuth:     noAuthRE.MatchString(inputMsg),
				})
			}
		}
	}
	// 校验 cmd_id 全局唯一
	seen := make(map[string]bool)
	for _, it := range items {
		if seen[it.CmdID] {
			return nil, fmt.Errorf("cmd_id %s is duplicated", it.CmdID)
		}
		seen[it.CmdID] = true
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CmdID < items[j].CmdID })
	return items, nil
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

func renderRoutes(routes []routeItem) string {
	var b strings.Builder
	b.WriteString("package rpc\n\n")
	b.WriteString("// RouteEntry 描述一条已生成的路由\n")
	b.WriteString("type RouteEntry struct {\n")
	b.WriteString("\tServerType uint32\n")
	b.WriteString("\tRoute string\n")
	b.WriteString("\tRspCmdID uint32\n")
	b.WriteString("}\n\n")
	b.WriteString("// RouteTable 是客户端入口路由表\n")
	b.WriteString("var RouteTable = map[uint32]RouteEntry{\n")
	for _, r := range routes {
		b.WriteString(fmt.Sprintf("\t%s: {ServerType: %s, Route: %q, RspCmdID: %s},\n", r.CmdID, r.ServerType, r.Route, r.RspCmdID))
	}
	b.WriteString("}\n\n")
	b.WriteString("// AuthWhitelist 表示免鉴权的 CmdID 集合\n")
	b.WriteString("var AuthWhitelist = map[uint32]bool{\n")
	for _, r := range routes {
		if r.NoAuth {
			b.WriteString(fmt.Sprintf("\t%s: true,\n", r.CmdID))
		}
	}
	b.WriteString("}\n")
	return b.String()
}
