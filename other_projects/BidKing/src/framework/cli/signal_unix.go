//go:build !windows

package cli

import (
	"fmt"
	"syscall"
)

func sendSignalTERM(pidFile string) error {
	return sendSignal(pidFile, syscall.SIGTERM)
}

func sendSignalKILL(pidFile string) error {
	return sendSignal(pidFile, syscall.SIGKILL)
}

func sendSignalHUP(pidFile string) error {
	return sendSignal(pidFile, syscall.SIGHUP)
}

func sendSignal(pidFile string, sig syscall.Signal) error {
	pid, err := readPID(pidFile)
	if err != nil {
		return err
	}
	if err := syscall.Kill(pid, sig); err != nil {
		return fmt.Errorf("kill pid=%d sig=%s: %w", pid, sig, err)
	}
	return nil
}
