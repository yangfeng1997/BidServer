package nodeid

import "testing"

func TestEncodeDecode(t *testing.T) {
	id := Encode(1, 2, 3)
	world, serverType, index := Decode(id.Uint32())
	if world != 1 || serverType != 2 || index != 3 {
		t.Fatalf("unexpected decode result: %d %d %d", world, serverType, index)
	}
	if got := id.String(); got != "1.2.3" {
		t.Fatalf("unexpected string: %s", got)
	}
}

func TestParse(t *testing.T) {
	id, err := Parse("4.5.6")
	if err != nil {
		t.Fatal(err)
	}
	if got := id.String(); got != "4.5.6" {
		t.Fatalf("unexpected string: %s", got)
	}
}
