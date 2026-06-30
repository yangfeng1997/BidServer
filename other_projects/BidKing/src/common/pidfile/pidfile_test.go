package pidfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"project/src/common/pidfile"
)

func TestWriteRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	pid := os.Getpid()

	if err := pidfile.Write(path, pid); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := pidfile.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != pid {
		t.Fatalf("got pid %d, want %d", got, pid)
	}
}

func TestRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	_ = pidfile.Write(path, os.Getpid())
	if err := pidfile.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file should be removed")
	}
}

func TestIsRunning_CurrentProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("IsRunning uses conservative false on Windows")
	}
	path := filepath.Join(t.TempDir(), "test.pid")
	_ = pidfile.Write(path, os.Getpid())
	if !pidfile.IsRunning(path) {
		t.Fatal("current process should be running")
	}
}

func TestIsRunning_NoPidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.pid")
	if pidfile.IsRunning(path) {
		t.Fatal("missing file should not be running")
	}
}

func TestRead_InvalidContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pid")
	_ = os.WriteFile(path, []byte("notanumber\n"), 0644)
	_, err := pidfile.Read(path)
	if err == nil {
		t.Fatal("expected error for invalid content")
	}
	var numErr *strconv.NumError
	if !errors.As(err, &numErr) {
		t.Fatalf("expected *strconv.NumError, got %T: %v", err, err)
	}
}
