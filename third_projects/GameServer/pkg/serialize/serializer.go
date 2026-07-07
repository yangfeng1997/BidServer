package serialize

type (
	// Marshaler 序列化接口
	Marshaler interface {
		Marshal(any) ([]byte, error)
	}

	// Unmarshaler 反序列化接口
	Unmarshaler interface {
		Unmarshal([]byte, any) error
	}

	// Serializer 序列化器接口，组合了序列化与反序列化能力
	Serializer interface {
		Marshaler
		Unmarshaler
	}
)
