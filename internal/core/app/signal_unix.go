//go:build !windows

package app

import (
	"os"
	"syscall"
)

func appSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM}
}

func isShutdownSignal(sig os.Signal) bool {
	return sig == syscall.SIGINT || sig == syscall.SIGQUIT
}

func isDrainShutdownSignal(sig os.Signal) bool {
	return sig == syscall.SIGTERM
}

func isReloadSignal(sig os.Signal) bool {
	return sig == syscall.SIGHUP
}
