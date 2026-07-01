package logger

import "time"

// FieldType 枚举所有支持的强类型，避免 interface{} 装箱
type FieldType uint8

const (
	StringType FieldType = iota
	Int64Type
	Int32Type
	Float64Type
	BoolType
	DurationType
	TimeType
	ErrorType
	AnyType
)

// Field 携带单条结构化日志字段，小对象尽量在栈上传递
type Field struct {
	Key       string
	Type      FieldType
	Integer   int64   // StringType/BoolType/DurationType/Int32Type 复用此字段
	Float     float64 // Float64Type
	String    string  // StringType
	Interface any     // ErrorType / AnyType / TimeType
}

// ---------- 构造函数 ----------

func String(key, val string) Field {
	return Field{Key: key, Type: StringType, String: val}
}

func Int(key string, val int) Field {
	return Field{Key: key, Type: Int64Type, Integer: int64(val)}
}

func Int32(key string, val int32) Field {
	return Field{Key: key, Type: Int32Type, Integer: int64(val)}
}

func Int64(key string, val int64) Field {
	return Field{Key: key, Type: Int64Type, Integer: val}
}

func Uint32(key string, val uint32) Field {
	return Field{Key: key, Type: Int64Type, Integer: int64(val)}
}

func Uint64(key string, val uint64) Field {
	return Field{Key: key, Type: Int64Type, Integer: int64(val)}
}

func Float64(key string, val float64) Field {
	return Field{Key: key, Type: Float64Type, Float: val}
}

func Bool(key string, val bool) Field {
	var i int64
	if val {
		i = 1
	}
	return Field{Key: key, Type: BoolType, Integer: i}
}

func Duration(key string, val time.Duration) Field {
	return Field{Key: key, Type: DurationType, Integer: int64(val)}
}

func Time(key string, val time.Time) Field {
	return Field{Key: key, Type: TimeType, Interface: val}
}

func Err(err error) Field {
	return Field{Key: "error", Type: ErrorType, Interface: err}
}

func NamedErr(key string, err error) Field {
	return Field{Key: key, Type: ErrorType, Interface: err}
}

// Any 兜底，仅在无强类型对应时使用（有反射开销）
func Any(key string, val any) Field {
	return Field{Key: key, Type: AnyType, Interface: val}
}
