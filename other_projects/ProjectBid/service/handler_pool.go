package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"strconv"

	"google.golang.org/protobuf/proto"

	"projectbid/server/conn/message"
	"projectbid/server/constants"
	pberrors "projectbid/server/errors"
	"projectbid/server/logger"
	"projectbid/server/pipeline"
	"projectbid/server/route"
	"projectbid/server/serialize"
	"projectbid/server/session"
)

// HandlerPool 管理所有已注册的 handler 方法，支持按路由查找和消息处理。
type HandlerPool struct {
	handlers map[string]*Handler
}

// NewHandlerPool 创建空的 handler 池。
func NewHandlerPool() *HandlerPool {
	return &HandlerPool{
		handlers: make(map[string]*Handler),
	}
}

// Register 注册一个 handler 方法。
func (h *HandlerPool) Register(serviceName, name string, handler *Handler) {
	h.handlers[fmt.Sprintf("%s.%s", serviceName, name)] = handler
}

// GetHandlers 返回所有已注册的 handler。
func (h *HandlerPool) GetHandlers() map[string]*Handler {
	return h.handlers
}

// ProcessHandlerMessage 处理一条消息：反序列化 → 前置管道 → 反射调用 → 后置管道 → 序列化响应。
func (h *HandlerPool) ProcessHandlerMessage(
	ctx context.Context,
	rt *route.Route,
	serializer serialize.Serializer,
	handlerHooks *pipeline.HandlerHooks,
	sess session.Session,
	data []byte,
	msgType message.Type,
) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	handler, err := h.getHandler(rt)
	if err != nil {
		return nil, err
	}

	exit, err := handler.ValidateMessageType(msgType)
	if err != nil && exit {
		return nil, fmt.Errorf("消息类型不匹配: %w", err)
	} else if err != nil {
		logger.Warnw("消息类型不匹配（非致命）", "错误", err)
	}

	// 反序列化参数
	arg, err := unmarshalHandlerArg(handler, serializer, data)
	if err != nil {
		return nil, pberrors.NewError(fmt.Errorf("反序列化 handler 参数失败: %w", err), pberrors.PIT498)
	}

	// 前置管道
	ctx, arg, err = handlerHooks.BeforeHandler.ExecuteBeforePipeline(ctx, arg)
	if err != nil {
		return nil, fmt.Errorf("前置管道执行失败: %w", err)
	}

	logger.Debugw("处理 handler 消息",
		"会话ID", sess.ID(),
		"路由", rt.String(),
	)

	// 构建反射调用参数
	args := []reflect.Value{handler.Receiver, reflect.ValueOf(ctx)}
	if arg != nil {
		args = append(args, reflect.ValueOf(arg))
	}

	resp, err := pcall(handler.Method, args)

	// 后置管道
	resp, err = handlerHooks.AfterHandler.ExecuteAfterPipeline(ctx, resp, err)
	if err != nil {
		return nil, fmt.Errorf("后置管道执行失败: %w", err)
	}

	ret, err := serializeReturn(serializer, resp)
	if err != nil {
		return nil, fmt.Errorf("序列化返回值失败: %w", err)
	}

	return ret, nil
}

func (h *HandlerPool) getHandler(rt *route.Route) (*Handler, error) {
	handler, ok := h.handlers[rt.Short()]
	if !ok {
		return nil, pberrors.NewError(fmt.Errorf("handler 未找到: %s", rt.String()), pberrors.PIT404)
	}
	return handler, nil
}

// ——— 辅助函数 ———

func unmarshalHandlerArg(handler *Handler, serializer serialize.Serializer, payload []byte) (interface{}, error) {
	if handler.IsRawArg {
		return payload, nil
	}

	if handler.Type != nil {
		arg := reflect.New(handler.Type.Elem()).Interface()
		// protobuf 消息直接调用 proto.Unmarshal
		if msg, ok := arg.(proto.Message); ok {
			if err := proto.Unmarshal(payload, msg); err != nil {
				return nil, err
			}
			return msg, nil
		}
		if err := serializer.Unmarshal(payload, arg); err != nil {
			return nil, err
		}
		return arg, nil
	}
	return nil, nil
}

func serializeReturn(ser serialize.Serializer, ret interface{}) ([]byte, error) {
	if data, ok := ret.([]byte); ok {
		return data, nil
	}
	// protobuf 消息直接调用 proto.Marshal
	if msg, ok := ret.(proto.Message); ok {
		return proto.Marshal(msg)
	}
	data, err := ser.Marshal(ret)
	if err != nil {
		logger.Errorw("序列化返回值失败", "错误", err)
		return nil, err
	}
	return data, nil
}

// pcall 安全地调用反射方法，捕获 panic 并转为 error。
func pcall(method reflect.Method, args []reflect.Value) (rets interface{}, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			stackTrace := debug.Stack()
			stackTraceStr := strconv.Quote(string(stackTrace))
			logger.Errorw("handler 方法 panic",
				"方法名", method.Name,
				"panic数据", rec,
				"堆栈", stackTraceStr,
			)

			if s, ok := rec.(string); ok {
				err = pberrors.NewError(errors.New(s), pberrors.PIT500)
			} else {
				err = pberrors.NewError(fmt.Errorf("handler 内部错误 - %s: %v", method.Name, rec), pberrors.PIT500)
			}
		}
	}()

	r := method.Func.Call(args)
	if len(r) == 2 {
		if v := r[1].Interface(); v != nil {
			err = v.(error)
		} else if !r[0].IsNil() {
			rets = r[0].Interface()
		} else {
			err = constants.ErrReplyShouldBeNotNull
		}
	}
	return
}
