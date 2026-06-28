package app

import (
	"os"
	"syscall"
)

func appSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func isShutdownSignal(os.Signal) bool {
	return true
}

func isDrainShutdownSignal(os.Signal) bool {
	return false
}

func isReloadSignal(os.Signal) bool {
	return false
}
