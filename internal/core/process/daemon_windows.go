//go:build windows

package process

import (
	"fmt"
	"os"
)

const daemonChildEnv = "GSP_DAEMON_CHILD"

func IsDaemonChild() bool {
	return os.Getenv(daemonChildEnv) == "1"
}

func StartDaemon() (bool, error) {
	return false, fmt.Errorf("daemon mode is not supported on windows")
}
