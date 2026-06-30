package json

import "encoding/json"

// Serializer 基于标准库 encoding/json 实现 serialize.Serializer 接口
type Serializer struct{}

// NewSerializer 返回一个新的 JSON 序列化器
func NewSerializer() *Serializer {
	return &Serializer{}
}

// Marshal 将 v 序列化为 JSON 字节切片
func (s *Serializer) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal 将 JSON 字节切片解析并存入 v 所指向的值
func (s *Serializer) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
