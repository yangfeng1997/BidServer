package daemon

import "strings"

// FilterArgs 过滤掉 --daemon/-d flag，用于 fork 子进程时去除后台化标志。
// 同时处理 pflag 的赋值形式，如 --daemon=true、--daemon=1。
func FilterArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--daemon" || strings.HasPrefix(a, "--daemon=") || a == "-d" {
			continue
		}
		out = append(out, a)
	}
	return out
}
