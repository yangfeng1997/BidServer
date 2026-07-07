package service

import (
	"context"
	"errors"
	"reflect"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	"projectbid/server/component"
	"projectbid/server/conn/message"
)

var (
	typeOfError         = reflect.TypeOf((*error)(nil)).Elem()
	typeOfBytes         = reflect.TypeOf(([]byte)(nil))
	typeOfContext       = reflect.TypeOf((*context.Context)(nil)).Elem()
	typeOfProtoMessage  = reflect.TypeOf((*proto.Message)(nil)).Elem()
)

// Handler 描述一个已注册的 handler 方法的元信息。
type Handler struct {
	Receiver    reflect.Value
	Method      reflect.Method
	Type        reflect.Type
	IsRawArg    bool
	MessageType message.Type
}

// Service 封装一个组件的反射信息与已提取的 handler 方法集合。
type Service struct {
	Name     string
	Type     reflect.Type
	Receiver reflect.Value
	Handlers map[string]*Handler
	Options  Options
}

// NewService 创建 Service 实例。
func NewService(comp component.Component, opts []Option) *Service {
	s := &Service{
		Type:     reflect.TypeOf(comp),
		Receiver: reflect.ValueOf(comp),
	}

	for i := range opts {
		opts[i](&s.Options)
	}
	if name := s.Options.Name; name != "" {
		s.Name = name
	} else {
		s.Name = reflect.Indirect(s.Receiver).Type().Name()
	}
	return s
}

// ExtractHandler 通过反射提取所有满足 handler 签名的方法。
func (s *Service) ExtractHandler() error {
	typeName := reflect.Indirect(s.Receiver).Type().Name()
	if typeName == "" {
		return errors.New("无法获取服务名，类型: " + s.Type.String())
	}
	if !isExported(typeName) {
		return errors.New("类型 " + typeName + " 未导出")
	}

	s.Handlers = suitableHandlerMethods(s.Type, s.Options.NameFunc)

	if len(s.Handlers) == 0 {
		str := ""
		methods := suitableHandlerMethods(reflect.PtrTo(s.Type), s.Options.NameFunc)
		if len(methods) != 0 {
			str = "类型 " + s.Name + " 没有导出的 handler 方法（提示: 请传递该类型的指针，且出参必须为 proto.Message）"
		} else {
			str = "类型 " + s.Name + " 没有导出的 handler 方法（出参必须为 proto.Message 或 []byte）"
		}
		return errors.New(str)
	}

	for i := range s.Handlers {
		s.Handlers[i].Receiver = s.Receiver
	}
	return nil
}

// ValidateMessageType 校验消息类型是否与 handler 声明的类型匹配。
func (h *Handler) ValidateMessageType(msgType message.Type) (exitOnError bool, err error) {
	if h.MessageType != msgType {
		switch msgType {
		case message.Request:
			return true, errors.New("收到 Request 类型的消息，但 handler 期望 Notify")
		case message.Notify:
			return false, errors.New("收到 Notify 类型的消息，但 handler 期望 Request")
		}
	}
	return
}

// ——— 反射辅助 ———

func isExported(name string) bool {
	w, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(w)
}

// isHandlerMethod 判断方法是否符合 handler 签名:
//
//	func(c *Comp, ctx context.Context, *Req) (*Resp, error)   // Request
//	func(c *Comp, ctx context.Context, *Req) error             // Notify
//	func(c *Comp, ctx context.Context) (*Resp, error)          // Request（无参数）
func isHandlerMethod(method reflect.Method) bool {
	mt := method.Type

	if method.PkgPath != "" {
		return false
	}

	// 需要 2 或 3 个入参: receiver, context.Context, 可选的 *T 或 []byte
	if mt.NumIn() != 2 && mt.NumIn() != 3 {
		return false
	}

	if !mt.In(1).Implements(typeOfContext) {
		return false
	}

	if mt.NumIn() == 3 && mt.In(2).Kind() != reflect.Ptr && mt.In(2) != typeOfBytes {
		return false
	}

	// 需要 0 或 2 个出参: (proto.Message 或 []byte) + error（Request），或空（Notify）
	if mt.NumOut() != 0 && mt.NumOut() != 2 {
		return false
	}

	if mt.NumOut() == 2 {
		if mt.Out(1) != typeOfError {
			return false
		}
		// 首个出参必须是 proto.Message 或 []byte（原始字节）
		if mt.Out(0) != typeOfBytes && !mt.Out(0).Implements(typeOfProtoMessage) {
			return false
		}
	}

	return true
}

func suitableHandlerMethods(typ reflect.Type, nameFunc func(string) string) map[string]*Handler {
	methods := make(map[string]*Handler)
	for m := 0; m < typ.NumMethod(); m++ {
		method := typ.Method(m)
		mt := method.Type
		mn := method.Name
		if isHandlerMethod(method) {
			raw := false
			if mt.NumIn() == 3 && mt.In(2) == typeOfBytes {
				raw = true
			}
			if nameFunc != nil {
				mn = nameFunc(mn)
			}
			var msgType message.Type
			if mt.NumOut() == 0 {
				msgType = message.Notify
			} else {
				msgType = message.Request
			}
			handler := &Handler{
				Method:      method,
				IsRawArg:    raw,
				MessageType: msgType,
			}
			if mt.NumIn() == 3 {
				handler.Type = mt.In(2)
			}
			methods[mn] = handler
		}
	}
	return methods
}
