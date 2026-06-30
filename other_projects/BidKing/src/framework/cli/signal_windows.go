//go:build windows

package cli

import "fmt"

func sendSignalTERM(_ string) error {
	return fmt.Errorf("stop/kill/reload commands are not supported on Windows")
}

func sendSignalKILL(_ string) error {
	return fmt.Errorf("stop/kill/reload commands are not supported on Windows")
}

func sendSignalHUP(_ string) error {
	return fmt.Errorf("stop/kill/reload commands are not supported on Windows")
}
