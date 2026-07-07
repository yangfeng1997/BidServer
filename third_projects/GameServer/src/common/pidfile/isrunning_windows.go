//go:build windows

package pidfile

import "os"

// IsRunning 在 Windows 上采用保守实现：
// Windows 不支持信号 0 探活，此处返回 false 以避免误判。
// 服务端实际运行在 Linux 上，使用 isrunning_unix.go 中的实现。
func IsRunning(path string) bool {
	pid, err := Read(path)
	if err != nil {
		return false
	}
	_, err = os.FindProcess(pid)
	// Windows 的 FindProcess 只要 pid > 0 就成功，无法真正探活，保守返回 false。
	_ = err
	return false
}
