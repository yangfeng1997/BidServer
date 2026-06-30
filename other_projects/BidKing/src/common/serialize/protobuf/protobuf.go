package protobuf

import (
	"errors"

	"google.golang.org/protobuf/proto"
)

// ErrWrongValueType 当传入值未实现 proto.Message 接口时返回此错误
var ErrWrongValueType = errors.New("protobuf: convert on wrong type value")

// Serializer 基于 google.golang.org/protobuf 实现 serialize.Serializer 接口
type Serializer struct{}

// NewSerializer 返回一个新的 Protobuf 序列化器
func NewSerializer() *Serializer {
	return &Serializer{}
}

// Marshal 将 v 序列化为 Protobuf 字节切片，v 必须实现 proto.Message 接口
func (s *Serializer) Marshal(v any) ([]byte, error) {
	pb, ok := v.(proto.Message)
	if !ok {
		return nil, ErrWrongValueType
	}
	return proto.Marshal(pb)
}

// Unmarshal 将 Protobuf 字节切片解析并存入 v 所指向的值，v 必须实现 proto.Message 接口
func (s *Serializer) Unmarshal(data []byte, v any) error {
	pb, ok := v.(proto.Message)
	if !ok {
		return ErrWrongValueType
	}
	return proto.Unmarshal(data, pb)
}
