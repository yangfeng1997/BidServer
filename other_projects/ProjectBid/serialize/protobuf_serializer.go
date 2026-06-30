package serialize

import (
	"errors"

	"google.golang.org/protobuf/proto"
)

// ErrWrongValueType 表示值类型不匹配。
var ErrWrongValueType = errors.New("值类型不匹配：需要 proto.Message")

// ProtobufSerializer 实现基于 protobuf 的序列化器。
type ProtobufSerializer struct{}

// NewProtobufSerializer 创建 Protobuf 序列化器。
func NewProtobufSerializer() *ProtobufSerializer {
	return &ProtobufSerializer{}
}

// GetName 返回 "protobuf"。
func (s *ProtobufSerializer) GetName() string { return "protobuf" }

// Marshal 返回 v 的 protobuf 编码。v 必须实现 proto.Message。
func (s *ProtobufSerializer) Marshal(v interface{}) ([]byte, error) {
	pb, ok := v.(proto.Message)
	if !ok {
		return nil, ErrWrongValueType
	}
	return proto.Marshal(pb)
}

// Unmarshal 将 protobuf 编码的数据解析到 v。v 必须实现 proto.Message。
func (s *ProtobufSerializer) Unmarshal(data []byte, v interface{}) error {
	pb, ok := v.(proto.Message)
	if !ok {
		return ErrWrongValueType
	}
	return proto.Unmarshal(data, pb)
}
