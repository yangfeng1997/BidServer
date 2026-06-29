package process

import (
	"fmt"
	"os"
)

func WritePIDFile(path string) error {
	if path == "" {
		return nil
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644)
}

func RemovePIDFile(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
