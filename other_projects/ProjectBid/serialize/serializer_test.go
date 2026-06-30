package serialize

import (
	"testing"
)

type testPayload struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func TestJSONMarshalUnmarshal(t *testing.T) {
	s := &JSONSerializer{}
	if s.GetName() != "json" {
		t.Errorf("GetName() = %s, want json", s.GetName())
	}

	original := &testPayload{Name: "hello", Value: 42}
	data, err := s.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}

	var restored testPayload
	if err := s.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}

	if restored.Name != original.Name || restored.Value != original.Value {
		t.Errorf("roundtrip 失败: %+v != %+v", restored, original)
	}
}
