package errcode

import "testing"

func TestCodeOf(t *testing.T) {
	if got := CodeOf(nil); got != OK {
		t.Fatalf("expected OK, got %v", got)
	}
	if got := CodeOf(New(ERR_TIMEOUT, "timeout")); got != ERR_TIMEOUT {
		t.Fatalf("expected timeout, got %v", got)
	}
}
