package routeragent

import (
	"path/filepath"
	"testing"
)

func TestUDSServerListenCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "routeragent", "ra.sock")
	s := NewUDSServer(path, nil)
	if err := s.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
