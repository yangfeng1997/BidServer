//go:build windows

package daemon

import "fmt"

// Daemonize 在 Windows 上不支持，返回错误。
// 服务端实际运行在 Linux 上。
func Daemonize() error {
	return fmt.Errorf("daemonize is not supported on Windows")
}
