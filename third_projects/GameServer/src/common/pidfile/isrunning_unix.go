//go:build !windows

package pidfile

import "syscall"

// IsRunning 通过向进程发送信号 0 探测 pidfile 中记录的进程是否仍在运行。
func IsRunning(path string) bool {
	pid, err := Read(path)
	if err != nil {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
