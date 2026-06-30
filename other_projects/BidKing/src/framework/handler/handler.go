package handler

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"unicode"
)

var (
	typeOfError   = reflect.TypeOf((*error)(nil)).Elem()
	typeOfBytes   = reflect.TypeOf(([]byte)(nil))
	typeOfContext = reflect.TypeOf((*context.Context)(nil)).Elem()
)

// Meta 一个合法 handler 方法的反射元数据
type Meta struct {
	Receiver reflect.Value
	Method   reflect.Method
	ArgType  reflect.Type // nil 表示无 req 参数
	IsRawArg bool         // ArgType == []byte，不走 serializer
	IsNotify bool         // NumOut == 0，不需要回包
}

// Extract 反射扫描 handler 对象的所有导出方法，提取合法 handler
// 合法签名：
//
//	func (s *T) Method(ctx context.Context, req *Req) (*Resp, error)
//	func (s *T) Method(ctx context.Context, req *Req)              // Notify
//	func (s *T) Method(ctx context.Context) (*Resp, error)         // 无参数
//	func (s *T) Method(ctx context.Context, raw []byte) (*Resp, error) // raw
//	func (s *T) Method(ctx context.Context, raw []byte)            // raw Notify
//
// nameFunc 对方法名做变换，默认 strings.ToLower
func Extract(handler any, nameFunc func(string) string) (map[string]*Meta, error) {
	if nameFunc == nil {
		nameFunc = strings.ToLower
	}

	typ := reflect.TypeOf(handler)
	if typ.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("handler object must be a pointer, got %s", typ.Kind())
	}

	typeName := typ.Elem().Name()
	if typeName == "" || !unicode.IsUpper(rune(typeName[0])) {
		return nil, fmt.Errorf("handler object type must be exported")
	}

	recv := reflect.ValueOf(handler)
	metas := make(map[string]*Meta)

	for i := 0; i < typ.NumMethod(); i++ {
		method := typ.Method(i)
		mt := method.Type

		if !isValidHandler(mt) {
			continue
		}

		meta := &Meta{
			Receiver: recv,
			Method:   method,
			IsNotify: mt.NumOut() == 0,
		}
		if mt.NumIn() == 3 {
			meta.ArgType = mt.In(2)
			meta.IsRawArg = mt.In(2) == typeOfBytes
		}

		name := nameFunc(method.Name)
		metas[name] = meta
	}

	if len(metas) == 0 {
		return nil, fmt.Errorf("no valid handler methods found on %s", typeName)
	}
	return metas, nil
}

// isValidHandler 校验方法签名是否合法
func isValidHandler(mt reflect.Type) bool {
	// 必须是导出方法（PkgPath 为空）
	// NumIn: receiver(1) + ctx(1) [+ req(1)] = 2 或 3
	if mt.NumIn() != 2 && mt.NumIn() != 3 {
		return false
	}
	// In(1) 必须实现 context.Context
	if !mt.In(1).Implements(typeOfContext) {
		return false
	}
	// In(2) 若存在，必须是 *T 或 []byte
	if mt.NumIn() == 3 {
		arg := mt.In(2)
		if arg != typeOfBytes && arg.Kind() != reflect.Ptr {
			return false
		}
	}
	// 返回值：0（Notify）或 2（ptr/[]byte + error）
	if mt.NumOut() != 0 && mt.NumOut() != 2 {
		return false
	}
	if mt.NumOut() == 2 {
		if mt.Out(1) != typeOfError {
			return false
		}
		out0 := mt.Out(0)
		if out0 != typeOfBytes && out0.Kind() != reflect.Ptr {
			return false
		}
	}
	return true
}
