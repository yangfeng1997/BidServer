package serialize

import "encoding/json"

// JSONSerializer 实现基于 encoding/json 的序列化器。
type JSONSerializer struct{}

// NewJSONSerializer 创建 JSON 序列化器。
func NewJSONSerializer() *JSONSerializer {
	return &JSONSerializer{}
}

// GetName 返回 "json"。
func (s *JSONSerializer) GetName() string { return "json" }

// Marshal 返回 v 的 JSON 编码。
func (s *JSONSerializer) Marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal 将 JSON 编码的数据解析到 v。
func (s *JSONSerializer) Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
