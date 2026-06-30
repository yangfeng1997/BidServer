// config_build 把 conf/ 配置模板烘焙为最终 config.yaml。
//
// 流程（单服务）：
//  1. 加载 common.yaml + {svc}.yaml 两棵 yaml 树（svc.yaml 可缺省）；
//  2. 深合并：svc 覆盖 common（map 递归，标量/列表整体覆盖），记录被覆盖的 key 原值；
//  3. 命名/填值：递归遍历，对标量上的占位符 ${...} 分类：
//     - 全小写 ${value} → 从 envs/{env}.yaml 取节点（整子树或标量）替换，记录来源；
//     替换后对结果再递归一次（处理 envs 自身带入的大写占位符）；
//     - 全大写 ${VAR}   → 保留（运行时环境注入），记录路径；
//     - 混合大小写       → 报错；
//     - 非占位符         → 原样；
//  4. 残留校验：填值后若仍有小写 ${value} → 报错；
//  5. env 交叉校验（envFields 非 nil 时）：大写 ${VAR} 必须在 EnvFields 中标记，
//     反之被标记 env 的字段必须填大写 ${VAR}；
//  6. 来源注释：给每个标量挂 LineComment（reloadable / override / from / env,runtime）；
//  7. 输出到 {run}/{svc}/conf/config.yaml。
//
// 用法：
//
//	go run ./tools/config_build --env=dev --svc=gatesvr
//	go run ./tools/config_build --env=dev --all
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// buildConfig 描述一次单服务烘焙所需的全部输入。
type buildConfig struct {
	confDir          string          // conf/ 根目录
	svc              string          // 服务名，如 "gatesvr"
	env              string          // 环境名，如 "dev"
	envsDir        string          // conf/envs/ 目录
	outDir           string          // run/{svc}/conf/ 目录
	envFields        map[string]bool // EnvFields[svcMsgName]；nil = 跳过 env 交叉校验（测试用）
	reloadableFields map[string]bool // ReloadableFields[svcMsgName]；nil = 注释中不标 reloadable
}

func main() {
	env := flag.String("env", "dev", "环境名，对应 conf/envs/{env}.yaml")
	svc := flag.String("svc", "", "服务名，对应 conf/{svc}.yaml；与 --all 二选一")
	all := flag.Bool("all", false, "构建 conf/ 下全部服务（除 common.yaml 与 envs/）")
	incremental := flag.Bool("incremental", false, "增量构建：仅当输入比产物新时才重建")
	confDir := flag.String("conf", "conf", "配置模板根目录")
	runDir := flag.String("run", "run", "产物根目录，输出到 {run}/{svc}/conf/config.yaml")
	flag.Parse()

	envsDir := filepath.Join(*confDir, "envs")

	var svcs []string
	if *all {
		names, err := listServices(*confDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config_build 列举服务失败: %v\n", err)
			os.Exit(1)
		}
		// common 始终作为第一个渲染，输出到 run/common/conf/
		svcs = append([]string{"common"}, names...)
	} else {
		if *svc == "" {
			fmt.Fprintln(os.Stderr, "config_build: 需指定 --svc 或 --all")
			os.Exit(1)
		}
		svcs = []string{*svc}
	}

	for _, name := range svcs {
		outDir := filepath.Join(*runDir, name, "conf")
		if *incremental && !needsRebuild(*confDir, name, *env, envsDir, outDir) {
			fmt.Fprintf(os.Stderr, "config_build: %s 无需重建（增量）\n", name)
			continue
		}
		cfg := buildConfig{
			confDir:          *confDir,
			svc:              name,
			env:              *env,
			envsDir:        envsDir,
			outDir:           outDir,
			envFields:        nil, // 真实表接入在 Task 6 之后由调用方处理
			reloadableFields: nil,
		}
		if err := build(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "config_build: 构建 %s 失败: %v\n", name, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "config_build: %s -> %s\n", name, filepath.Join(outDir, "config.yaml"))
	}
}

// listServices 列举 conf/ 下的服务名（{svc}.yaml，排除 common.yaml）。
func listServices(confDir string) ([]string, error) {
	entries, err := os.ReadDir(confDir)
	if err != nil {
		return nil, fmt.Errorf("读 conf 目录 %s: %w", confDir, err)
	}
	var svcs []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		if name == "common.yaml" {
			continue
		}
		svcs = append(svcs, strings.TrimSuffix(name, ".yaml"))
	}
	return svcs, nil
}

// needsRebuild 基于 mtime 判断产物是否需要重建。
// svc=common 的输入源 = common.yaml + envs/{env}.yaml；
// 其他服务的输入源 = {svc}.yaml + envs/{env}.yaml（不含 common.yaml）。
// 产物不存在则需重建。
func needsRebuild(confDir, svc, env, envsDir, outDir string) bool {
	out := filepath.Join(outDir, "config.yaml")
	outInfo, err := os.Stat(out)
	if err != nil {
		return true // 产物缺失
	}
	outMod := outInfo.ModTime()
	var srcs []string
	if svc == "common" {
		srcs = []string{
			filepath.Join(confDir, "common.yaml"),
			filepath.Join(envsDir, env+".yaml"),
		}
	} else {
		srcs = []string{
			filepath.Join(confDir, svc+".yaml"),
			filepath.Join(envsDir, env+".yaml"),
		}
	}
	for _, s := range srcs {
		info, err := os.Stat(s)
		if err != nil {
			continue // 源缺失不触发重建
		}
		if info.ModTime().After(outMod) {
			return true
		}
	}
	return false
}

// build 执行单服务的完整烘焙流程。
// svc="common" 时渲染 common.yaml，其他时只渲染 {svc}.yaml（不合并 common）。
func build(cfg buildConfig) error {
	var tree *yaml.Node
	var err error

	if cfg.svc == "common" {
		tree, err = loadYAMLNode(filepath.Join(cfg.confDir, "common.yaml"))
		if err != nil {
			return fmt.Errorf("加载 common.yaml: %w", err)
		}
	} else {
		tree, err = loadYAMLNode(filepath.Join(cfg.confDir, cfg.svc+".yaml"))
		if err != nil {
			return fmt.Errorf("加载 %s.yaml: %w", cfg.svc, err)
		}
	}

	envs, err := loadYAMLNode(filepath.Join(cfg.envsDir, cfg.env+".yaml"))
	if err != nil {
		return fmt.Errorf("加载 envs/%s.yaml: %w", cfg.env, err)
	}

	fillLog := map[string]string{}
	upperPaths := map[string]bool{}
	resolving := map[string]bool{}
	if err := validateAndFill(tree, envs, cfg.env, "", fillLog, upperPaths, resolving); err != nil {
		return err
	}

	if err := checkResidual(tree); err != nil {
		return err
	}

	if cfg.envFields != nil {
		if err := checkEnvCrossConsistency(tree, cfg.envFields, ""); err != nil {
			return err
		}
	}

	attachComments(tree, "", map[string]string{}, fillLog, upperPaths, cfg.reloadableFields)

	if err := os.MkdirAll(cfg.outDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录 %s: %w", cfg.outDir, err)
	}
	out := filepath.Join(cfg.outDir, "config.yaml")
	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("创建 %s: %w", out, err)
	}
	defer f.Close()
	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	if err := enc.Encode(tree); err != nil {
		return fmt.Errorf("写 %s: %w", out, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("关闭 encoder: %w", err)
	}
	return nil
}

// loadYAMLNode 读 yaml 成 *yaml.Node（取 DocumentNode.Content[0]）。
// 文件不存在或内容为空时返回空 MappingNode（svc.yaml / envs 可缺省）。
func loadYAMLNode(path string) (*yaml.Node, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyMapping(), nil
		}
		return nil, fmt.Errorf("读 %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", path, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		// 空文件 / 仅注释：返回空 mapping。
		return emptyMapping(), nil
	}
	return doc.Content[0], nil
}

// emptyMapping 构造一个空的 MappingNode。
func emptyMapping() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

// deepMergeWithLog 深合并 base 与 overlay：map 递归合并，标量/列表由 overlay 整体覆盖。
// 被 overlay 覆盖的路径会记录到 overrideLog[path] = base 原值（标量取其字符串值）。
// prefix 是当前节点的点路径前缀（顶层为空）。返回合并后的新树（不修改入参）。
func deepMergeWithLog(base, overlay *yaml.Node, prefix string, overrideLog map[string]string) *yaml.Node {
	// 只有双方都是 MappingNode 才递归合并；否则 overlay 覆盖。
	if base == nil {
		return overlay
	}
	if overlay == nil {
		return base
	}
	if base.Kind != yaml.MappingNode || overlay.Kind != yaml.MappingNode {
		// 标量/列表覆盖：记录 base 原值（仅标量时有意义）。
		if base.Kind == yaml.ScalarNode {
			overrideLog[prefix] = base.Value
		} else {
			overrideLog[prefix] = "<non-scalar>"
		}
		return overlay
	}

	// 双 mapping：以 base 为起点逐 key 合并。
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: base.Tag, Style: base.Style}
	// 收集 base 的 key 顺序与节点。
	baseVals := map[string]*yaml.Node{}
	var keyOrder []string
	for i := 0; i+1 < len(base.Content); i += 2 {
		k := base.Content[i].Value
		baseVals[k] = base.Content[i+1]
		keyOrder = append(keyOrder, k)
	}
	overlayKeys := map[string]*yaml.Node{}
	overlayKeyNode := map[string]*yaml.Node{}
	for i := 0; i+1 < len(overlay.Content); i += 2 {
		k := overlay.Content[i].Value
		overlayKeys[k] = overlay.Content[i+1]
		overlayKeyNode[k] = overlay.Content[i]
		if _, ok := baseVals[k]; !ok {
			keyOrder = append(keyOrder, k) // overlay 新增的 key 追加到末尾
		}
	}

	for _, k := range keyOrder {
		childPath := k
		if prefix != "" {
			childPath = prefix + "." + k
		}
		bv, hasBase := baseVals[k]
		ov, hasOverlay := overlayKeys[k]

		// 构造该 key 的 keyNode（优先沿用 base 的，以保留其注释/位置；否则用 overlay 的）。
		var keyNode *yaml.Node
		for i := 0; i+1 < len(base.Content); i += 2 {
			if base.Content[i].Value == k {
				keyNode = base.Content[i]
				break
			}
		}
		if keyNode == nil {
			keyNode = overlayKeyNode[k]
		}

		var valNode *yaml.Node
		switch {
		case hasBase && hasOverlay:
			valNode = deepMergeWithLog(bv, ov, childPath, overrideLog)
		case hasBase:
			valNode = bv
		default:
			valNode = ov
		}
		out.Content = append(out.Content, keyNode, valNode)
	}
	return out
}

// placeholderKind 占位符类别。
type placeholderKind int

const (
	pkNone  placeholderKind = iota // 非占位符
	pkLower                        // 全小写 ${value}
	pkUpper                        // 全大写 ${VAR}
	pkMixed                        // 混合大小写 ${Mixed}
)

// placeholderRe 匹配整串恰为一个占位符 ${...}。
var placeholderRe = regexp.MustCompile(`^\$\{([^}]+)\}$`)

// classifyPlaceholder 判断 val 是否整串为占位符，并按 name 的大小写归类。
// 返回类别与占位符名（pkNone 时 name 为空）。
func classifyPlaceholder(val string) (placeholderKind, string) {
	m := placeholderRe.FindStringSubmatch(val)
	if m == nil {
		return pkNone, ""
	}
	name := m[1]
	hasLower, hasUpper := false, false
	for _, r := range name {
		if unicode.IsLower(r) {
			hasLower = true
		}
		if unicode.IsUpper(r) {
			hasUpper = true
		}
	}
	switch {
	case hasLower && hasUpper:
		return pkMixed, name
	case hasUpper:
		return pkUpper, name
	default:
		// 全小写，或仅含数字/下划线等无大小写字符：归为 lower（需 envs 填充）。
		return pkLower, name
	}
}

// lookupValueNode 按点路径 key 在 envs 树中查找节点。
// 支持整子树（"redis" → mapping）与标量（"log.level" → scalar）。找不到返回 nil。
func lookupValueNode(envs *yaml.Node, key string) *yaml.Node {
	if envs == nil {
		return nil
	}
	cur := envs
	parts := strings.Split(key, ".")
	for _, p := range parts {
		if cur == nil || cur.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(cur.Content); i += 2 {
			if cur.Content[i].Value == p {
				next = cur.Content[i+1]
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

// validateAndFill 递归遍历 node，分类并填充占位符。
//   - MappingNode：对每个 value 递归，childPath = path 拼 key；
//   - SequenceNode：对每个子节点递归（path 不变）；
//   - ScalarNode：classifyPlaceholder 后处理：
//     pkMixed → 报错；
//     pkLower → 在 envs 查节点，找不到报错；找到则整子树深拷贝替换并记录 fillLog，
//     再对替换后的 node 递归一次（处理 envs 带入的大写占位符）；
//     pkUpper → 保留，记录 upperPaths；
//     pkNone  → 原样。
//
// resolving 记录当前正在解析的小写占位符 name 集合，用于检测自引用 / 互引用成环：
// 进入某个 pkLower name 的填充前若 name 已在集合中 → 返回环引用错误（防栈溢出）；
// 填充完成后从集合移除。
func validateAndFill(node, envs *yaml.Node, env, path string, fillLog map[string]string, upperPaths map[string]bool, resolving map[string]bool) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := node.Content[i].Value
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			if err := validateAndFill(node.Content[i+1], envs, env, childPath, fillLog, upperPaths, resolving); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, c := range node.Content {
			if err := validateAndFill(c, envs, env, path, fillLog, upperPaths, resolving); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		kind, name := classifyPlaceholder(node.Value)
		switch kind {
		case pkMixed:
			return fmt.Errorf("路径 %q 的占位符 ${%s} 为混合大小写（mixed case），不允许：小写=envs 填充，大写=运行时注入", path, name)
		case pkLower:
			// 环检测：name 正在解析途中又指向自身 → 自引用 / 互引用成环。
			if resolving[name] {
				return fmt.Errorf("路径 %q 的占位符 ${%s} 构成环引用（circular placeholder reference），无法解析", path, name)
			}
			valNode := lookupValueNode(envs, name)
			if valNode == nil {
				return fmt.Errorf("路径 %q 的占位符 ${%s} 在 envs/%s.yaml 中无对应值（missing）", path, name, env)
			}
			resolving[name] = true
			// 整子树替换：深拷贝 valNode 后复制进 node，避免与 envs 树共享指针
			// （别名地雷），后续原地改写不会串改到 envs / base 树。
			cp := deepCopyNode(valNode)
			node.Kind = cp.Kind
			node.Tag = cp.Tag
			node.Value = cp.Value
			node.Style = cp.Style
			node.Content = cp.Content
			fillLog[path] = "envs/" + env + ".yaml"
			// 对替换后的 node 再递归一次（处理 envs 带入的大写占位符；
			// 小写占位符会被残留校验拦截）。
			if err := validateAndFill(node, envs, env, path, fillLog, upperPaths, resolving); err != nil {
				return err
			}
			delete(resolving, name)
		case pkUpper:
			upperPaths[path] = true
		case pkNone:
			// 原样保留。
		}
	}
	return nil
}

// deepCopyNode 递归深拷贝一个 yaml.Node（Kind/Tag/Value/Style 及 Content 子树）。
// 用于整子树替换时复制 envs 节点，避免填入的子树与 envs 树共享指针。
func deepCopyNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	cp := &yaml.Node{
		Kind:  n.Kind,
		Tag:   n.Tag,
		Value: n.Value,
		Style: n.Style,
	}
	if len(n.Content) > 0 {
		cp.Content = make([]*yaml.Node, len(n.Content))
		for i, c := range n.Content {
			cp.Content[i] = deepCopyNode(c)
		}
	}
	return cp
}

// checkEnvCrossConsistency 递归校验 yaml 中大写占位符与 envFields 的一致性。
//   - 标量是大写 ${VAR} 但 path 未在 envFields 中 → 报错；
//   - path 在 envFields 中但标量不是大写 ${VAR} → 报错。
func checkEnvCrossConsistency(node *yaml.Node, envFields map[string]bool, path string) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := node.Content[i].Value
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			if err := checkEnvCrossConsistency(node.Content[i+1], envFields, childPath); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, c := range node.Content {
			if err := checkEnvCrossConsistency(c, envFields, path); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		kind, _ := classifyPlaceholder(node.Value)
		isUpper := kind == pkUpper
		marked := envFields[path]
		if isUpper && !marked {
			return fmt.Errorf("路径 %q 填了大写运行时占位符 %s，但 proto 未标记 env=true（env 交叉校验失败）", path, node.Value)
		}
		if marked && !isUpper {
			return fmt.Errorf("路径 %q 被 proto 标记为 env=true，但 yaml 写的是字面量 %q 而非大写 ${VAR}（env 交叉校验失败）", path, node.Value)
		}
	}
	return nil
}

// checkResidual 递归扫描，发现填值后仍残留的小写占位符 → 报错。
func checkResidual(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		for _, c := range node.Content {
			if err := checkResidual(c); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		if kind, name := classifyPlaceholder(node.Value); kind == pkLower {
			return fmt.Errorf("残留未填充的小写占位符 ${%s}（值 %q 来自 envs 注入或模板，无法解析）", name, node.Value)
		}
	}
	return nil
}

// attachComments 递归给标量挂来源注释（LineComment）。
// 按顺序拼接 parts：
//   - reloadable[path]            → "reloadable"
//   - overrideLog[path] 存在       → "override ← svc.yaml (common: <orig>)"
//   - upperPaths[path]            → "env, runtime"；否则 fillLog[path] 存在 → "from <src>"
//
// 有 parts 时设置 node.LineComment = "# " + strings.Join(parts, " | ")。
func attachComments(node *yaml.Node, path string, overrideLog, fillLog map[string]string, upperPaths map[string]bool, reloadable map[string]bool) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := node.Content[i].Value
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			attachComments(node.Content[i+1], childPath, overrideLog, fillLog, upperPaths, reloadable)
		}
	case yaml.SequenceNode:
		for _, c := range node.Content {
			attachComments(c, path, overrideLog, fillLog, upperPaths, reloadable)
		}
	case yaml.ScalarNode:
		var parts []string
		if reloadable[path] {
			parts = append(parts, "reloadable")
		}
		if orig, ok := overrideLog[path]; ok {
			parts = append(parts, fmt.Sprintf("override ← svc.yaml (common: %s)", orig))
		}
		if upperPaths[path] {
			parts = append(parts, "env, runtime")
		} else if src, ok := fillLog[path]; ok {
			parts = append(parts, "from "+src)
		}
		if len(parts) > 0 {
			node.LineComment = "# " + strings.Join(parts, " | ")
		}
	}
}
