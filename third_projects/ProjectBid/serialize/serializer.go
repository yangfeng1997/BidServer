// Package serialize 定义消息序列化/反序列化接口，并提供 JSON 和 Protobuf 实现。
package serialize

// Serializer 定义消息序列化/反序列化接口。
type Serializer interface {
	// GetName 返回序列化器名称（如 "json"、"protobuf"），用于握手协商。
	GetName() string
	// Marshal 将 Go 值序列化为字节数组。
	Marshal(v interface{}) ([]byte, error)
	// Unmarshal 将字节数组反序列化为 Go 值（必须是指针）。
	Unmarshal(data []byte, v interface{}) error
}
