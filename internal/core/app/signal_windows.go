package app

import (
	"os"
	"syscall"
)

func appSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func isReloadSignal(os.Signal) bool {
	return false
}
