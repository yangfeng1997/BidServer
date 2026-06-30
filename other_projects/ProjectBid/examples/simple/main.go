package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"projectbid/server/application"
	"projectbid/server/component"
	"projectbid/server/config"
	"projectbid/server/conn/codec"
	"projectbid/server/conn/message"
	"projectbid/server/acceptor"
	"projectbid/server/examples/simple/pb"
	"projectbid/server/group"
	"projectbid/server/serialize"
	"projectbid/server/session"
	"projectbid/server/timer"
)

// ——— Handler 组件 ———

// GreetHandler 提供 Greeting 相关的 RPC 方法。
// 导出的方法自动被发现并注册为 handler:
//   - SayHello: 客户端发 "GreetHandler.SayHello" 调用
//   - Ping: 客户端发 "GreetHandler.Ping" 调用（Notify 类型）
type GreetHandler struct {
	component.Base
	groupService group.GroupService
}

func (h *GreetHandler) Name() string { return "GreetHandler" }

// SayHello 是 Request/Response 类型的 handler，出参为 proto.Message。
// 客户端发送 protobuf 编码的 HelloRequest，返回 HelloResponse。
func (h *GreetHandler) SayHello(ctx context.Context, req *pb.HelloRequest) (*pb.HelloResponse, error) {
	return &pb.HelloResponse{Message: "你好, " + req.Name + "!"}, nil
}

// Ping 是 Notify 类型的 handler（无返回值）。
// 客户端发送 Notify 消息即可，无需等待响应。
func (h *GreetHandler) Ping(ctx context.Context) error {
	sess := ctx.Value("pitaya.session").(session.Session)
	fmt.Printf("[GreetHandler] Ping 来自会话 %d\n", sess.ID())
	return nil
}

// ——— 业务 Module ———

// ChatModule 演示业务模块。
type ChatModule struct {
	component.Base
	sessionPool session.SessionPool
	groups      group.GroupService
	timeWheel   *timer.TimeWheel
}

func (m *ChatModule) Name() string { return "chat" }

func (m *ChatModule) Init(ctx context.Context) error {
	fmt.Println("[chat] 初始化聊天模块...")
	// 创建默认房间分组
	m.groups.NewGroup("lobby")
	return nil
}

func (m *ChatModule) AfterInit(ctx context.Context) error {
	// 启动一个定时器，每秒打印在线人数
	m.timeWheel.AddEveryFunc(30*time.Second, func() {
		count := m.sessionPool.GetSessionCount()
		fmt.Printf("[chat] 在线人数: %d\n", count)
	})

	// 每 5 秒向大厅广播一条服务器消息
	m.timeWheel.AddEveryFunc(5*time.Second, func() {
		g := m.groups.GetGroup("lobby")
		if g != nil && g.Count() > 0 {
			g.Broadcast("OnServerAnnounce", map[string]interface{}{
				"msg":       "服务器定期公告",
				"timestamp": time.Now().Unix(),
			})
		}
	})
	return nil
}

func (m *ChatModule) BeforeShutdown(ctx context.Context) error {
	fmt.Println("[chat] 停止接收消息...")
	return nil
}

func (m *ChatModule) Shutdown(ctx context.Context) error {
	fmt.Println("[chat] 关闭所有分组...")
	return nil
}

// ——— 管道钩子 ———

func loggingBeforeHook(ctx context.Context, in interface{}) (context.Context, interface{}, error) {
	fmt.Printf("[管道] 前置钩子: 收到请求 %T\n", in)
	return ctx, in, nil
}

func loggingAfterHook(ctx context.Context, out interface{}, err error) (interface{}, error) {
	if err != nil {
		fmt.Printf("[管道] 后置钩子: 处理失败, 错误=%v\n", err)
	} else {
		fmt.Printf("[管道] 后置钩子: 处理成功\n")
	}
	return out, err
}

// ——— main ———

func main() {
	// 创建基础编码器和序列化器
	packetDecoder := codec.NewPomeloPacketDecoder()
	packetEncoder := &codec.PomeloPacketEncoder{}
	serializer := &serialize.ProtobufSerializer{}

	msgEncoder := message.NewMessagesEncoder(false)

	// 创建 GroupService 和 TimeWheel（在 Builder 外部以便注入到 Module）
	groupSvc := group.NewGroupService()

	// ——— 配置方式一：从 Viper YAML 文件加载 ———
	//
	// cfg, err := config.NewConfigFromFile("config.yaml")
	// if err != nil {
	//     panic(err)
	// }
	// builder := application.NewBuilder(true, func(c *config.Config) { *c = cfg })
	//
	// config.yaml 示例:
	//   name: simple-server
	//   version: "1.0.0"
	//   heartbeat:
	//     interval: 30s
	//   buffer:
	//     writeTimeout: 10s
	//     messagesBufferSize: 64
	//     localProcessBufferSize: 100
	//   acceptor:
	//     addr: ":8000"
	//     transport: tcp
	//   cluster:
	//     natsURL: nats://localhost:4222
	//     etcd:
	//       endpoints: [127.0.0.1:2379]
	//       prefix: projectbid
	//       heartbeatTTL: 30
	//       syncServersInterval: 30
	//   timer:
	//     enabled: true
	//     tick: 10ms
	//     wheelSize: 20

	// ——— 配置方式二：函数式选项程序化构造 ———
	builder := application.NewBuilder(
		true, // isFrontend: 前端服务，启用客户端连接监听
		config.WithName("simple-server"),
		config.WithVersion("1.0.0"),
		config.WithGracefulTimeout(10*time.Second),
	)

	app, err := builder.
		// 启用网络监听
		EnableAcceptor(acceptor.Options{
			Addr:               ":8000",
			PacketDecoder:      packetDecoder,
			PacketEncoder:      packetEncoder,
			MessageEncoder:     msgEncoder,
			Serializer:         serializer,
			HeartbeatTimeout:   30 * time.Second,
			WriteTimeout:       10 * time.Second,
			MessagesBufferSize: 64,
		}).
		// 启用时间轮
		EnableTimeWheel(10*time.Millisecond, 20).
		// 注册管道钩子
		AddBeforeHandlerHook(loggingBeforeHook).
		AddAfterHandlerHook(loggingAfterHook).
		// 注册业务 Module
		AddService(
			&ChatService{
				sessionPool: builder.GetSessionPool(),
			},
		).
		AddModule(
			&ChatModule{
				sessionPool: builder.GetSessionPool(),
				groups:      groupSvc,
			},
		).
		// 关闭前回调
		OnShutdown(func() {
			fmt.Println("[app] 刷新指标...")
		}).
		OnStartup(func() {
			fmt.Println("[app] 所有组件已就绪，开始接受连接")
			builder.GetHandlerService().DumpServices()
		}).
		Build()

	if err != nil {
		fmt.Fprintf(os.Stderr, "构建失败: %v\n", err)
		os.Exit(1)
	}

	// 注册 Handler 组件（对齐 Pitaya: Build() 后 Register，Start() 前完成注册）
	if err := app.Register(&GreetHandler{groupService: groupSvc}); err != nil {
		fmt.Fprintf(os.Stderr, "注册 handler 失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[app] %s v%s 启动中...\n", app.Name(), app.Version())

	// 3 秒后自动关闭（演示用）
	go func() {
		fmt.Println("[app] 将在 10 秒后自动关闭...")
		<-time.After(10 * time.Second)
		fmt.Println("[app] 触发关闭")
		app.Shutdown()
	}()

	if err := app.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[app] 最终状态: %s\n", app.State())
	fmt.Println("[app] 干净退出")
}

// ChatService 基础设施服务，负责管理 TCP acceptor 和 handler dispatch。
type ChatService struct {
	component.Base
	sessionPool session.SessionPool
}

func (s *ChatService) Name() string { return "chat-service" }

// 注意：实际的 acceptor 启动和 handler dispatch 由 Application 框架管理，
// ChatService 仅作为示例展示基础设施 Service 的注册方式。
