//go:build !windows

package app

import (
	"os"
	"syscall"
)

func appSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
}

func isReloadSignal(sig os.Signal) bool {
	return sig == syscall.SIGHUP
}
