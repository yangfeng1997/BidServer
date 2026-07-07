package transport

import (
	"context"
	"errors"
	"fmt"
	"math/rand"

	"google.golang.org/protobuf/proto"
	"project/src/common/logger"
	"project/src/framework/cluster"
	"project/src/framework/cluster/discovery"
	"project/src/framework/cluster/pb"
)

type NatsCluster struct {
	discovery *discovery.Discovery
	rpc       *NatsRPC
	self      cluster.NodeID
	dieCh     chan struct{}
}

type NatsClusterConfig struct {
	EtcdEndpoints  []string
	NatsURLs       []string
	SelfAddr       string
	ServerTypeName string
	DieChan        chan struct{}
	AsyncDispatch bool // 入站消息并发处理（仅 router 设 true）
}

func NewNatsCluster(self cluster.NodeID, cfg NatsClusterConfig) (*NatsCluster, error) {
	dieCh := cfg.DieChan
	if dieCh == nil {
		dieCh = make(chan struct{}, 1)
	}
	disc, err := discovery.NewDiscovery(cfg.EtcdEndpoints, self, cfg.ServerTypeName, cfg.SelfAddr, dieCh)
	if err != nil {
		return nil, fmt.Errorf("discovery init failed: %w", err)
	}
	rpc, err := NewNatsRPC(cfg.NatsURLs, self, dieCh, cfg.AsyncDispatch)
	if err != nil {
		return nil, fmt.Errorf("nats rpc init failed: %w", err)
	}
	return &NatsCluster{discovery: disc, rpc: rpc, self: self, dieCh: dieCh}, nil
}

var _ cluster.Cluster = (*NatsCluster)(nil)

func (c *NatsCluster) Name() string { return "nats" }

func (c *NatsCluster) Init() error {
	if err := c.discovery.Init(); err != nil {
		return err
	}
	return c.rpc.Start()
}

func (c *NatsCluster) Stop() error {
	c.rpc.Stop()
	return c.discovery.Stop()
}

// Call 指定节点，异步有返回，done(respBytes, err) 在 NATS goroutine 执行
func (c *NatsCluster) Call(ctx context.Context, target cluster.NodeID, route string, req proto.Message, done func([]byte, error)) {
	data, err := proto.Marshal(req)
	if err != nil {
		done(nil, fmt.Errorf("marshal req failed: %w", err))
		return
	}
	c.rpc.CallAsync(ctx, target, buildMsg(ctx, route, data), done)
}

// CallRaw 指定节点，异步有返回，data 为已序列化的 []byte（转发场景用）
func (c *NatsCluster) CallRaw(ctx context.Context, target cluster.NodeID, route string, data []byte, done func([]byte, error)) {
	c.rpc.CallAsync(ctx, target, buildMsg(ctx, route, data), done)
}

// CallRawSync 指定节点，同步有返回，data 为已序列化 []byte（转发场景用）
func (c *NatsCluster) CallRawSync(ctx context.Context, target cluster.NodeID, route string, data []byte) ([]byte, error) {
	return c.rpc.Call(ctx, target, buildMsg(ctx, route, data))
}

// CallSync 指定节点，同步有返回
func (c *NatsCluster) CallSync(ctx context.Context, target cluster.NodeID, route string, req proto.Message) ([]byte, error) {
	data, err := proto.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal req failed: %w", err)
	}
	return c.rpc.Call(ctx, target, buildMsg(ctx, route, data))
}

// Cast 指定节点，无返回
func (c *NatsCluster) Cast(ctx context.Context, target cluster.NodeID, route string, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal msg failed: %w", err)
	}
	return c.rpc.Send(ctx, target, buildMsg(ctx, route, data))
}

// CastRaw 指定节点，无返回，data 为已序列化的 []byte（转发场景用）
func (c *NatsCluster) CastRaw(ctx context.Context, target cluster.NodeID, route string, data []byte) error {
	return c.rpc.Send(ctx, target, buildMsg(ctx, route, data))
}

// CallAny 随机节点，异步有返回
func (c *NatsCluster) CallAny(ctx context.Context, serverTypeName string, route string, req proto.Message, done func([]byte, error)) {
	target, err := c.pickOne(serverTypeName)
	if err != nil {
		done(nil, err)
		return
	}
	c.Call(ctx, target, route, req, done)
}

// CallAnyRaw 随机节点，异步有返回，data 为已序列化的 []byte
func (c *NatsCluster) CallAnyRaw(ctx context.Context, serverTypeName string, route string, data []byte, done func([]byte, error)) {
	target, err := c.pickOne(serverTypeName)
	if err != nil {
		done(nil, err)
		return
	}
	c.CallRaw(ctx, target, route, data, done)
}

// CallAnySync 随机节点，同步有返回
func (c *NatsCluster) CallAnySync(ctx context.Context, serverTypeName string, route string, req proto.Message) ([]byte, error) {
	target, err := c.pickOne(serverTypeName)
	if err != nil {
		return nil, err
	}
	return c.CallSync(ctx, target, route, req)
}

// CastAny 随机节点，无返回
func (c *NatsCluster) CastAny(ctx context.Context, serverTypeName string, route string, msg proto.Message) error {
	target, err := c.pickOne(serverTypeName)
	if err != nil {
		return err
	}
	return c.Cast(ctx, target, route, msg)
}

// CastAnyRaw 随机节点，无返回，data 为已序列化的 []byte
func (c *NatsCluster) CastAnyRaw(ctx context.Context, serverTypeName string, route string, data []byte) error {
	target, err := c.pickOne(serverTypeName)
	if err != nil {
		return err
	}
	return c.CastRaw(ctx, target, route, data)
}

// Broadcast 广播到该类型所有节点，失败只打日志，不影响其他节点
func (c *NatsCluster) Broadcast(ctx context.Context, serverTypeName string, route string, msg proto.Message) {
	nodes := c.discovery.ByType(serverTypeName)
	if len(nodes) == 0 {
		logger.Warn("cluster broadcast: no nodes found", logger.String("type", serverTypeName))
		return
	}
	data, err := proto.Marshal(msg)
	if err != nil {
		logger.Error("cluster broadcast: marshal failed", logger.Err(err))
		return
	}
	clusterMsg := buildMsg(ctx, route, data)
	for _, node := range nodes {
		target, err := cluster.ParseNodeID(node.NodeId)
		if err != nil {
			logger.Warn("cluster broadcast: invalid nodeID", logger.String("nodeID", node.NodeId))
			continue
		}
		if err := c.rpc.Send(ctx, target, clusterMsg); err != nil {
			logger.Warn("cluster broadcast: send failed",
				logger.String("target", node.NodeId),
				logger.String("route", route),
				logger.Err(err))
		}
	}
}

func (c *NatsCluster) Discovery() *discovery.Discovery      { return c.discovery }
func (c *NatsCluster) AddSDListener(l discovery.SDListener) { c.discovery.AddListener(l) }
func (c *NatsCluster) DieChan() <-chan struct{}             { return c.dieCh }

// SetHandler 实现 HandlerSetter 接口，由 Application.Start() 自动注入
func (c *NatsCluster) SetHandler(h cluster.MessageHandler) { c.rpc.SetHandler(h) }

func (c *NatsCluster) pickOne(serverTypeName string) (cluster.NodeID, error) {
	nodes := c.discovery.ByType(serverTypeName)
	if len(nodes) == 0 {
		return 0, fmt.Errorf("no nodes found for type: %s", serverTypeName)
	}
	return cluster.ParseNodeID(nodes[rand.Intn(len(nodes))].NodeId)
}

func buildMsg(ctx context.Context, route string, data []byte) *pb.ClusterMessage {
	msg := &pb.ClusterMessage{Route: route, Data: data}
	if sess := cluster.SessionFromCtx(ctx); sess != nil {
		msg.Session = sess
	}
	return msg
}

var ErrNoNodes = errors.New("no available nodes")
