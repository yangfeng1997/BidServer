package rpc

import (
	"fmt"
	"reflect"
	"sync/atomic"

	"google.golang.org/protobuf/proto"

	"project/internal/core/errcode"
)

// RouteHandler handles one decoded RPC route payload.
type RouteHandler func(ctx Ctx, body []byte, reply func([]byte, error)) error

// Dispatcher dispatches RouterAgent route strings to typed service adapters.
type Dispatcher struct {
	handlers map[string]RouteHandler
}

// NewDispatcher creates an empty route dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: make(map[string]RouteHandler)}
}

// Register binds a route string to a handler.
func (d *Dispatcher) Register(route string, h RouteHandler) error {
	if d == nil {
		return errcode.New(errcode.ERR_INTERNAL, "rpc dispatcher is nil")
	}
	if route == "" {
		return errcode.New(errcode.ERR_NO_ROUTE, "rpc route is empty")
	}
	if h == nil {
		return errcode.New(errcode.ERR_INTERNAL, "nil handler for route: "+route)
	}
	if _, exists := d.handlers[route]; exists {
		return errcode.New(errcode.ERR_INTERNAL, "duplicate rpc route: "+route)
	}
	d.handlers[route] = h
	return nil
}

// MustRegister binds a route string to a handler and panics on invalid registration.
func (d *Dispatcher) MustRegister(route string, h RouteHandler) {
	if err := d.Register(route, h); err != nil {
		panic(err)
	}
}

// Dispatch invokes a registered route handler.
func (d *Dispatcher) Dispatch(route string, ctx Ctx, body []byte, reply func([]byte, error)) error {
	if d == nil {
		return errcode.New(errcode.ERR_NO_ROUTE, "rpc dispatcher is nil")
	}
	h := d.handlers[route]
	if h == nil {
		return errcode.New(errcode.ERR_NO_ROUTE, "route not found: "+route)
	}
	return h(ctx, body, OnceReply(reply))
}

// RecoverRoute converts a route handler panic into ERR_INTERNAL.
func RecoverRoute(route string, h RouteHandler) RouteHandler {
	return func(ctx Ctx, body []byte, reply func([]byte, error)) (err error) {
		defer func() {
			if v := recover(); v != nil {
				err = errcode.New(errcode.ERR_INTERNAL, fmt.Sprintf("rpc route %s panic: %v", route, v))
			}
		}()
		return h(ctx, body, reply)
	}
}

// OnceReply wraps reply so only the first call is delivered.
func OnceReply(reply func([]byte, error)) func([]byte, error) {
	if reply == nil {
		return nil
	}
	var called atomic.Bool
	return func(payload []byte, err error) {
		if called.Swap(true) {
			return
		}
		reply(payload, err)
	}
}

// ReplyProto marshals typed protobuf responses into a raw RPC reply.
func ReplyProto[T proto.Message](reply func([]byte, error)) Reply[T] {
	reply = OnceReply(reply)
	return func(rsp T, err error) {
		if reply == nil {
			return
		}
		if err != nil {
			reply(nil, err)
			return
		}
		if isNilProtoMessage(rsp) {
			reply(nil, errcode.New(errcode.ERR_INTERNAL, "nil response"))
			return
		}
		payload, merr := proto.Marshal(rsp)
		if merr != nil {
			reply(nil, errcode.New(errcode.ERR_INTERNAL, merr.Error()))
			return
		}
		reply(payload, nil)
	}
}

func isNilProtoMessage(m proto.Message) bool {
	if m == nil {
		return true
	}
	v := reflect.ValueOf(m)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
