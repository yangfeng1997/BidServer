//go:build !windows

package process

import (
	"os"
	"os/exec"
	"syscall"
)

const daemonChildEnv = "GSP_DAEMON_CHILD"

func IsDaemonChild() bool {
	return os.Getenv(daemonChildEnv) == "1"
}

func StartDaemon() (bool, error) {
	if IsDaemonChild() {
		return false, nil
	}

	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Env = append(os.Environ(), daemonChildEnv+"=1")
	cmd.Dir, _ = os.Getwd()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return false, err
	}
	return true, cmd.Process.Release()
}
