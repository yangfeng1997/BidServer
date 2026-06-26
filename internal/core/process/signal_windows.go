package process

import (
	"fmt"
	"os"
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
		return proc.Kill()
	case "reload":
		return fmt.Errorf("reload signal is not supported on windows")
	default:
		return fmt.Errorf("unknown signal %q", signal)
	}
}
