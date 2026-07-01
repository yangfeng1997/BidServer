package protobuf

import (
	"errors"

	"google.golang.org/protobuf/proto"
)

// 非 proto.Message 时返回
var ErrWrongValueType = errors.New("protobuf: convert on wrong type value")

// protobuf 序列化器
type Serializer struct{}

// NewSerializer 返回一个新的 Protobuf 序列化器
func NewSerializer() *Serializer {
	return &Serializer{}
}

// // 序列化为 Protobuf 字节切片
func (s *Serializer) Marshal(v any) ([]byte, error) {
	pb, ok := v.(proto.Message)
	if !ok {
		return nil, ErrWrongValueType
	}
	return proto.Marshal(pb)
}

// // 反序列化 Protobuf 字节切片
func (s *Serializer) Unmarshal(data []byte, v any) error {
	pb, ok := v.(proto.Message)
	if !ok {
		return ErrWrongValueType
	}
	return proto.Unmarshal(data, pb)
}
