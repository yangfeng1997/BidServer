package handler

import (
	"context"
	"fmt"
	"project/src/common/serialize"
	ferrors "project/src/framework/errors"
	"project/src/framework/pipeline"
	"reflect"
	"runtime/debug"
	"sync"
)

// Registry handler 注册表，维护 route → Meta 映射和全局/per-route Pipeline
type Registry struct {
	mu         sync.RWMutex
	handlers   map[string]*Meta
	pipes      map[string]*pipeline.Pipeline
	global     *pipeline.Pipeline
	serializer serialize.Serializer
}

func NewRegistry(serializer serialize.Serializer) *Registry {
	return &Registry{
		handlers:   make(map[string]*Meta),
		pipes:      make(map[string]*pipeline.Pipeline),
		global:     pipeline.New(),
		serializer: serializer,
	}
}

// RegisterHandler 反射扫描 handler 对象，以 "TypeName.methodname" 为 route 注册所有合法 handler 方法
func (r *Registry) RegisterHandler(handler any, nameFunc func(string) string) error {
	metas, err := Extract(handler, nameFunc)
	if err != nil {
		return err
	}
	typeName := reflect.TypeOf(handler).Elem().Name()
	r.mu.Lock()
	for name, meta := range metas {
		route := typeName + "." + name
		if _, exists := r.handlers[route]; exists {
			r.mu.Unlock()
			return fmt.Errorf("handler already registered: %s", route)
		}
		r.handlers[route] = meta
	}
	r.mu.Unlock()
	return nil
}

// HasRoute 报告是否注册了该 route 的 handler（gate 据此决定本地处理还是转发）
func (r *Registry) HasRoute(route string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.handlers[route]
	return ok
}

func (r *Registry) UseBefore(fns ...pipeline.BeforeFunc) { r.global.UseBefore(fns...) }
func (r *Registry) UseAfter(fns ...pipeline.AfterFunc)   { r.global.UseAfter(fns...) }

func (r *Registry) UseRouteBefore(route string, fns ...pipeline.BeforeFunc) {
	r.mu.Lock()
	r.getOrCreatePipe(route).UseBefore(fns...)
	r.mu.Unlock()
}

func (r *Registry) UseRouteAfter(route string, fns ...pipeline.AfterFunc) {
	r.mu.Lock()
	r.getOrCreatePipe(route).UseAfter(fns...)
	r.mu.Unlock()
}

func (r *Registry) getOrCreatePipe(route string) *pipeline.Pipeline {
	if r.pipes[route] == nil {
		r.pipes[route] = pipeline.New()
	}
	return r.pipes[route]
}

// SessionProvider handler 调用时需要的连接能力
type SessionProvider interface {
	Push(msgID uint32, data []byte) error
	Response(mid uint32, msgID uint32, data []byte) error
	// ResponseErr 回带框架错误码的 Response（无 data），code 为负数
	ResponseErr(mid uint32, msgID uint32, code int32) error
	SessionID() int64
	UID() int64
	IP() string
	FrontendID() string
}

// ctx key 类型
type ctxSessionIDKey struct{}
type ctxUIDKey struct{}
type ctxIPKey struct{}
type ctxFrontendIDKey struct{}

// ctxGet 泛型辅助，从 ctx 取指定类型的值，不存在时返回零值
func ctxGet[T any](ctx context.Context, key any) T {
	v, _ := ctx.Value(key).(T)
	return v
}

func SessionIDFromCtx(ctx context.Context) int64   { return ctxGet[int64](ctx, ctxSessionIDKey{}) }
func UIDFromCtx(ctx context.Context) int64         { return ctxGet[int64](ctx, ctxUIDKey{}) }
func IPFromCtx(ctx context.Context) string         { return ctxGet[string](ctx, ctxIPKey{}) }
func FrontendIDFromCtx(ctx context.Context) string { return ctxGet[string](ctx, ctxFrontendIDKey{}) }

func injectSession(ctx context.Context, sp SessionProvider) context.Context {
	ctx = context.WithValue(ctx, ctxSessionIDKey{}, sp.SessionID())
	ctx = context.WithValue(ctx, ctxUIDKey{}, sp.UID())
	ctx = context.WithValue(ctx, ctxIPKey{}, sp.IP())
	ctx = context.WithValue(ctx, ctxFrontendIDKey{}, sp.FrontendID())
	return ctx
}

// WithSessionID 注入 sessionID 到 ctx，供自定义注入点与测试使用。
// 生产路径由 injectSession 自动注入；本函数让外部能构造带 sessionID 的 ctx。
func WithSessionID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxSessionIDKey{}, id)
}

// Dispatch 按 route 查找 Meta，经 pipeline 后反射调用 handler
func (r *Registry) Dispatch(ctx context.Context, sp SessionProvider, route string, mid uint32, respMsgID uint32, data []byte) error {
	r.mu.RLock()
	meta, ok := r.handlers[route]
	pipe := r.pipes[route]
	r.mu.RUnlock()

	if !ok {
		// 路由缺失：若是 Request（respMsgID>0）回 NotFound 错误码给客户端
		if respMsgID > 0 {
			_ = sp.ResponseErr(mid, respMsgID, ferrors.NotFound)
		}
		return fmt.Errorf("no handler for route: %s", route)
	}

	ctx = injectSession(ctx, sp)

	var handlerErr error

	ctx, data, err := r.global.ExecuteBefore(ctx, data)
	if err != nil {
		handlerErr = err
		goto after
	}
	if pipe != nil {
		ctx, data, err = pipe.ExecuteBefore(ctx, data)
		if err != nil {
			handlerErr = err
			goto after
		}
	}

	handlerErr = invoke(ctx, meta, r.serializer, sp, mid, respMsgID, data)

after:
	if pipe != nil {
		handlerErr = pipe.ExecuteAfter(ctx, handlerErr)
	}
	handlerErr = r.global.ExecuteAfter(ctx, handlerErr)
	return handlerErr
}

// DispatchCluster 处理集群 RPC 消息，经 pipeline 后反射调用 handler，直接返回序列化后的 resp
func (r *Registry) DispatchCluster(ctx context.Context, route string, data []byte) ([]byte, error) {
	r.mu.RLock()
	meta, ok := r.handlers[route]
	pipe := r.pipes[route]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no handler for route: %s", route)
	}

	var handlerErr error
	var respBytes []byte

	ctx, data, err := r.global.ExecuteBefore(ctx, data)
	if err != nil {
		handlerErr = err
		goto after
	}
	if pipe != nil {
		ctx, data, err = pipe.ExecuteBefore(ctx, data)
		if err != nil {
			handlerErr = err
			goto after
		}
	}

	respBytes, handlerErr = invokeCluster(ctx, meta, r.serializer, data)

after:
	if pipe != nil {
		handlerErr = pipe.ExecuteAfter(ctx, handlerErr)
	}
	handlerErr = r.global.ExecuteAfter(ctx, handlerErr)
	return respBytes, handlerErr
}

// invokeCore 反射调用公共核心：unmarshal → pcall → marshal resp bytes
// invoke 和 invokeCluster 共用，消除重复代码
func invokeCore(ctx context.Context, meta *Meta, ser serialize.Serializer, data []byte) ([]byte, error) {
	args := []reflect.Value{meta.Receiver, reflect.ValueOf(ctx)}
	if meta.ArgType != nil {
		if meta.IsRawArg {
			args = append(args, reflect.ValueOf(data))
		} else {
			ptr := reflect.New(meta.ArgType.Elem()).Interface()
			if err := ser.Unmarshal(data, ptr); err != nil {
				return nil, fmt.Errorf("unmarshal req failed: %w", err)
			}
			args = append(args, reflect.ValueOf(ptr))
		}
	}
	results, err := pcall(meta.Method, args)
	if err != nil {
		return nil, err
	}
	if meta.IsNotify || len(results) == 0 {
		return nil, nil
	}
	resp := results[0].Interface()
	if resp == nil {
		return nil, nil
	}
	if b, ok := resp.([]byte); ok {
		return b, nil
	}
	respBytes, err := ser.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal resp failed: %w", err)
	}
	return respBytes, nil
}

// invoke 反射调用 handler：经 invokeCore → sp.Response 回包。
// handler 出错时，Request 类型回带框架错误码的 Response；Notify 不回包。
func invoke(ctx context.Context, meta *Meta, ser serialize.Serializer, sp SessionProvider, mid uint32, respMsgID uint32, data []byte) error {
	respBytes, err := invokeCore(ctx, meta, ser, data)
	if err != nil {
		// Notify 无需回包，仅向上返回供日志记录
		if meta.IsNotify {
			return err
		}
		// Request：回框架错误码（普通 error → Internal，*errors.Error → 其 Code）
		_ = sp.ResponseErr(mid, respMsgID, ferrors.CodeOf(err))
		return err
	}
	if meta.IsNotify || respBytes == nil {
		return nil
	}
	return sp.Response(mid, respMsgID, respBytes)
}

// invokeCluster 集群 RPC 反射调用，直接返回序列化后的 resp bytes
func invokeCluster(ctx context.Context, meta *Meta, ser serialize.Serializer, data []byte) ([]byte, error) {
	return invokeCore(ctx, meta, ser, data)
}

// pcall 带 panic 保护的反射调用，recover 后记录完整 stack trace
func pcall(method reflect.Method, args []reflect.Value) ([]reflect.Value, error) {
	var results []reflect.Value
	var err error

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				err = fmt.Errorf("handler panic: %v\n%s", rec, debug.Stack())
			}
		}()
		results = method.Func.Call(args)
	}()

	if err != nil {
		return nil, err
	}
	if len(results) == 2 {
		if e := results[1].Interface(); e != nil {
			if herr, ok := e.(error); ok {
				return nil, herr
			}
			return nil, fmt.Errorf("unexpected handler return type: %T(%v)", e, e)
		}
	}
	return results, nil
}
