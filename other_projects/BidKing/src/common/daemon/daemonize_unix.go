//go:build !windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

// Daemonize 将当前进程转为后台进程：
//  1. 过滤 --daemon/-d，重新 exec 自身
//  2. 子进程通过 setsid 脱离终端
//  3. 父进程退出
func Daemonize() error {
	args := FilterArgs(os.Args[1:])
	cmd := exec.Command(os.Args[0], args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
