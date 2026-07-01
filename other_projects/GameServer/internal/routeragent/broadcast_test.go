package routeragent

import "testing"

func TestBroadcastWaiter(t *testing.T) {
	w := NewBroadcastWaiter(1, []uint32{10, 20, 30})
	if w.Done() {
		t.Error("should not be done initially")
	}
	w.Mark(10)
	w.Mark(20)
	if w.Done() {
		t.Error("should not be done with 2/3")
	}
	w.Mark(30)
	if !w.Done() {
		t.Error("should be done")
	}
}

func TestBroadcastSentCodec(t *testing.T) {
	data := EncodeBroadcastSent(42, []uint32{1, 2, 3})
	id, nodes, err := DecodeBroadcastSent(data)
	if err != nil {
		t.Fatalf("DecodeBroadcastSent: %v", err)
	}
	if id != 42 {
		t.Errorf("id=%d", id)
	}
	if len(nodes) != 3 || nodes[0] != 1 {
		t.Errorf("nodes=%v", nodes)
	}
}
