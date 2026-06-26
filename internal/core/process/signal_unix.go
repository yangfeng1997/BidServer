//go:build !windows

package process

import (
	"fmt"
	"os"
	"syscall"
)

func SignalProcess(pidFile string, signal string) error {
	pid, err := ReadPIDFile(pidFile)
	if err != nil {
		return err
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	switch signal {
	case "stop":
		return proc.Signal(syscall.SIGTERM)
	case "reload":
		return proc.Signal(syscall.SIGHUP)
	default:
		return fmt.Errorf("unknown signal %q", signal)
	}
}
