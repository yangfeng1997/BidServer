package routeragent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// etcd 序列化的节点信息
type nodeInfoJSON struct {
	NodeID     uint32 `json:"node_id"`
	ServerType uint32 `json:"server_type"`
	RAAddr     string `json:"ra_addr"`
	StartAt    int64  `json:"start_at"`
}

// etcd 注册中心
type EtcdRegistry struct {
	cli    *clientv3.Client
	prefix string
	nodeID uint32
	raAddr string
	stopCh chan struct{}
}

// 创建 etcd 注册中心
func NewEtcdRegistry(endpoints []string, prefix string) (*EtcdRegistry, error) {
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("etcd endpoints is empty")
	}
	if prefix == "" {
		prefix = "/routeragent/nodes"
	}
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("etcd client: %w", err)
	}
	return &EtcdRegistry{
		cli:    cli,
		prefix: prefix,
		stopCh: make(chan struct{}),
	}, nil
}

// 注册本节点到 etcd，启动 keepalive
func (r *EtcdRegistry) Register(nodeID uint32, raAddr string, serverType uint32) error {
	r.nodeID = nodeID
	r.raAddr = raAddr
	key := fmt.Sprintf("%s/%d", r.prefix, nodeID)
	info := nodeInfoJSON{
		NodeID:     nodeID,
		ServerType: serverType,
		RAAddr:     raAddr,
		StartAt:    time.Now().Unix(),
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	// 创建 lease
	lease, err := r.cli.Grant(context.Background(), 10)
	if err != nil {
		return fmt.Errorf("etcd grant: %w", err)
	}
	// put with lease
	_, err = r.cli.Put(context.Background(), key, string(data), clientv3.WithLease(lease.ID))
	if err != nil {
		return fmt.Errorf("etcd put: %w", err)
	}
	// keepalive
	ch, err := r.cli.KeepAlive(context.Background(), lease.ID)
	if err != nil {
		return fmt.Errorf("etcd keepalive: %w", err)
	}
	go func() {
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					return
				}
			case <-r.stopCh:
				_, _ = r.cli.Revoke(context.Background(), lease.ID)
				return
			}
		}
	}()
	return nil
}

// 从 etcd 拉取节点并持续 watch
func (r *EtcdRegistry) Discover(onAdd func(NodeInfo), onDel func(uint32)) error {
	ctx := context.Background()
	// 初始全量加载
	resp, err := r.cli.Get(ctx, r.prefix, clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("etcd get: %w", err)
	}
	for _, kv := range resp.Kvs {
		var info nodeInfoJSON
		if err := json.Unmarshal(kv.Value, &info); err != nil {
			continue
		}
		onAdd(NodeInfo{
			NodeID:  info.NodeID,
			RAAddr:  info.RAAddr,
			StartAt: info.StartAt,
		})
	}
	// watch 增量变更
	go func() {
		watchCh := r.cli.Watch(ctx, r.prefix, clientv3.WithPrefix())
		for {
			select {
			case <-r.stopCh:
				return
			case wresp, ok := <-watchCh:
				if !ok {
					return
				}
				for _, ev := range wresp.Events {
					switch ev.Type {
					case clientv3.EventTypePut:
						var info nodeInfoJSON
						if err := json.Unmarshal(ev.Kv.Value, &info); err != nil {
							continue
						}
						onAdd(NodeInfo{
							NodeID:  info.NodeID,
							RAAddr:  info.RAAddr,
							StartAt: info.StartAt,
						})
					case clientv3.EventTypeDelete:
						var nodeID uint32
						fmt.Sscanf(path.Base(string(ev.Kv.Key)), "%d", &nodeID)
						onDel(nodeID)
					}
				}
			}
		}
	}()
	return nil
}

// 按 nodeID 查询 RA 地址
func (r *EtcdRegistry) PeerAddr(nodeID uint32) (string, bool) {
	resp, err := r.cli.Get(context.Background(), fmt.Sprintf("%s/%d", r.prefix, nodeID))
	if err != nil || len(resp.Kvs) == 0 {
		return "", false
	}
	var info nodeInfoJSON
	if err := json.Unmarshal(resp.Kvs[0].Value, &info); err != nil {
		return "", false
	}
	return info.RAAddr, true
}

// 关闭 etcd 连接
func (r *EtcdRegistry) Close() {
	select {
	case <-r.stopCh:
	default:
		close(r.stopCh)
	}
	_ = r.cli.Close()
}

// 从环境变量读取 etcd 地址
func EtcdEndpointsFromEnv() []string {
	if v := os.Getenv("ETCD_ENDPOINTS"); v != "" {
		return []string{v}
	}
	return nil
}
