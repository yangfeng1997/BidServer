package application

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"google.golang.org/protobuf/proto"

	"projectbid/server/cluster"
	"projectbid/server/conn/message"
	"projectbid/server/route"
	"projectbid/server/service"
)

type rpcCallerEntry struct {
	serverType string
	target     interface{}
	opts       []service.Option
}

// AddRPCCaller 注册一个 RPC 调用方代理。
//
// target 必须是指向 struct 的指针，struct 的每个 func 类型字段都将被替换为
// NATS RPC 代理函数。字段名即远程方法名。
//
// 服务名默认从 struct 类型名去掉 "Client" 后缀推导，可通过 service.WithName 覆盖。
//
// 代理函数签名需满足:
//
//	func(ctx context.Context, req *Input) (*Output, error)   // Request
//	func(ctx context.Context) error                           // Notify
//
// 用法:
//
//	type RoomClient struct {
//	    Join  func(ctx context.Context, req *pb.JoinReq) (*pb.JoinResp, error)
//	    Leave func(ctx context.Context) error
//	}
//
//	builder.AddRPCCaller("room-server", &RoomClient{})
func (b *Builder) AddRPCCaller(serverType string, target interface{}, opts ...service.Option) *Builder {
	b.rpcCallers = append(b.rpcCallers, rpcCallerEntry{
		serverType: serverType,
		target:     target,
		opts:       opts,
	})
	return b
}

func wireRPCCallers(b *Builder, natsClient *cluster.NatsRPCClient) error {
	for _, entry := range b.rpcCallers {
		if err := wireRPCProxy(natsClient, entry); err != nil {
			return err
		}
	}
	return nil
}

func wireRPCProxy(client *cluster.NatsRPCClient, entry rpcCallerEntry) error {
	v := reflect.ValueOf(entry.target)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("AddRPCCaller: target 必须是指向 struct 的指针，收到 %T", entry.target)
	}

	structVal := v.Elem()
	structType := structVal.Type()

	// 推导服务名：优先 WithName，否则 struct 类型名去掉 "Client" 后缀
	svcOpts := &service.Options{}
	for _, o := range entry.opts {
		o(svcOpts)
	}
	svcName := svcOpts.Name
	if svcName == "" {
		svcName = strings.TrimSuffix(structType.Name(), "Client")
	}

	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		fieldVal := structVal.Field(i)

		if field.Type.Kind() != reflect.Func {
			continue
		}

		if !fieldVal.CanSet() {
			return fmt.Errorf("AddRPCCaller: 字段 %s 不可设置（是否传递了指针？）", field.Name)
		}

		methodName := field.Name
		proxy, err := makeProxyFunc(client, entry.serverType, svcName, methodName, field.Type)
		if err != nil {
			return fmt.Errorf("AddRPCCaller: 字段 %s: %w", field.Name, err)
		}
		fieldVal.Set(proxy)
	}

	return nil
}

func makeProxyFunc(client *cluster.NatsRPCClient, serverType, svcName, methodName string, fnType reflect.Type) (reflect.Value, error) {
	// 验证签名
	numIn := fnType.NumIn()
	numOut := fnType.NumOut()
	if numIn < 1 || numIn > 2 {
		return reflect.Value{}, fmt.Errorf("签名需为 func(ctx, *Req) (*Resp, error) 或 func(ctx) error，实为 %s", fnType)
	}
	if numOut < 1 || numOut > 2 {
		return reflect.Value{}, fmt.Errorf("签名需为 func(ctx, *Req) (*Resp, error) 或 func(ctx) error，实为 %s", fnType)
	}
	if fnType.Out(numOut-1).Kind() != reflect.Interface {
		return reflect.Value{}, fmt.Errorf("最后一个出参需为 error")
	}

	isNotify := numOut == 1
	hasInput := numIn == 2

	return reflect.MakeFunc(fnType, func(args []reflect.Value) []reflect.Value {
		ctx := args[0].Interface().(context.Context)

		var data []byte
		if hasInput {
			in := args[1].Interface()
			var err error
			if pm, ok := in.(proto.Message); ok {
				data, err = proto.Marshal(pm)
			} else {
				data, err = json.Marshal(in)
			}
			if err != nil {
				return makeReturn(fnType, isNotify, fmt.Errorf("序列化 %s.%s 请求失败: %w", svcName, methodName, err))
			}
		}

		msgType := message.Request
		if isNotify {
			msgType = message.Notify
		}

		rt := &route.Route{SvType: serverType, Service: svcName, Method: methodName}
		msg := &message.Message{Type: msgType, Route: rt.String(), Data: data}
		resp, err := client.Call(ctx, cluster.RPCTypeUser, rt, nil, msg, &cluster.Server{Type: serverType})
		if err != nil {
			return makeReturn(fnType, isNotify, err)
		}
		if resp.Error != nil && resp.Error.Msg != "" {
			errMsg := resp.Error.Msg
			if resp.Error.Code != "" {
				errMsg = resp.Error.Code + " - " + resp.Error.Msg
			}
			return makeReturn(fnType, isNotify, fmt.Errorf("%s.%s 远程错误: %s", svcName, methodName, errMsg))
		}

		if isNotify {
			return []reflect.Value{reflect.Zero(fnType.Out(0))} // nil error
		}

		// 反序列化响应
		outType := fnType.Out(0)
		outVal := reflect.New(outType.Elem())
		outIface := outVal.Interface()

		if pm, ok := outIface.(proto.Message); ok {
			if err := proto.Unmarshal(resp.Data, pm); err != nil {
				zero := reflect.Zero(outType)
				return []reflect.Value{zero, reflect.ValueOf(fmt.Errorf("反序列化 %s.%s 响应失败: %w", svcName, methodName, err))}
			}
			return []reflect.Value{outVal, reflect.Zero(fnType.Out(1))}
		}

		if err := json.Unmarshal(resp.Data, outIface); err != nil {
			zero := reflect.Zero(outType)
			return []reflect.Value{zero, reflect.ValueOf(fmt.Errorf("反序列化 %s.%s 响应失败: %w", svcName, methodName, err))}
		}
		return []reflect.Value{outVal, reflect.Zero(fnType.Out(1))}
	}), nil
}

func makeReturn(fnType reflect.Type, isNotify bool, err error) []reflect.Value {
	if isNotify {
		return []reflect.Value{reflect.ValueOf(err)}
	}
	zero := reflect.Zero(fnType.Out(0))
	return []reflect.Value{zero, reflect.ValueOf(err)}
}
