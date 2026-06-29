package process

import (
	"os"
	"syscall"
)

func WatchedSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP}
}

func IsTerminateSignal(sig os.Signal) bool {
	return sig == syscall.SIGINT || sig == syscall.SIGQUIT
}

func IsDrainSignal(sig os.Signal) bool {
	return sig == syscall.SIGTERM
}

func IsReloadSignal(sig os.Signal) bool {
	return sig == syscall.SIGHUP
}
