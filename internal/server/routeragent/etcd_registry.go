package routeragent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"project/internal/core/nodeid"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// etcd 序列化的节点信息
type nodeInfoJSON struct {
	NodeID     string `json:"node_id"`
	ServerType uint32 `json:"server_type"`
	RAAddr     string `json:"ra_addr"`
	StartAt    int64  `json:"start_at"`
}

// EtcdRegistry 保存注册与发现状态
type EtcdRegistry struct {
	cli    *clientv3.Client
	prefix string
	lease  clientv3.LeaseID
	nodeID uint32
	raAddr string
	stopCh chan struct{}
	mu     sync.Mutex
}

// NodePrefix 返回当前集群和 world 下的节点发现前缀。
func NodePrefix(clusterName, clusterEnv string, worldID uint32) string {
	if clusterName == "" {
		clusterName = "bidserver"
	}
	return fmt.Sprintf("/%s/%s/worlds/%d/nodes", clusterName, clusterEnv, worldID)
}

// NewEtcdRegistry 创建 etcd 注册中心
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
	return &EtcdRegistry{cli: cli, prefix: prefix, stopCh: make(chan struct{})}, nil
}

// Register 注册本节点到 etcd
func (r *EtcdRegistry) Register(nodeID uint32, raAddr string, serverType uint32) error {
	r.nodeID = nodeID
	r.raAddr = raAddr
	return r.PutNode(nodeID, raAddr, serverType)
}

// PutNode 使用当前 lease 注册节点到 etcd
func (r *EtcdRegistry) PutNode(nodeID uint32, raAddr string, serverType uint32) error {
	lease, err := r.ensureLease()
	if err != nil {
		return err
	}
	key := r.nodeKey(nodeID)
	info := nodeInfoJSON{NodeID: nodeid.String(nodeID), ServerType: serverType, RAAddr: raAddr, StartAt: time.Now().Unix()}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if _, err = r.cli.Put(context.Background(), key, string(data), clientv3.WithLease(lease)); err != nil {
		return fmt.Errorf("etcd put: %w", err)
	}
	return nil
}

// DeleteNode 删除节点注册
func (r *EtcdRegistry) DeleteNode(nodeID uint32) error {
	_, err := r.cli.Delete(context.Background(), r.nodeKey(nodeID))
	return err
}

func (r *EtcdRegistry) ensureLease() (clientv3.LeaseID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lease != 0 {
		return r.lease, nil
	}
	lease, err := r.cli.Grant(context.Background(), 10)
	if err != nil {
		return 0, fmt.Errorf("etcd grant: %w", err)
	}
	ch, err := r.cli.KeepAlive(context.Background(), lease.ID)
	if err != nil {
		return 0, fmt.Errorf("etcd keepalive: %w", err)
	}
	r.lease = lease.ID
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
	return lease.ID, nil
}

func (r *EtcdRegistry) nodeKey(nodeID uint32) string {
	return fmt.Sprintf("%s/%s", r.prefix, nodeid.String(nodeID))
}

type decodedNodeInfo struct {
	node       NodeInfo
	serverType uint32
}

func decodeNodeInfo(data []byte) (decodedNodeInfo, bool) {
	var info nodeInfoJSON
	if err := json.Unmarshal(data, &info); err != nil {
		return decodedNodeInfo{}, false
	}
	id, err := nodeid.Parse(info.NodeID)
	if err != nil {
		return decodedNodeInfo{}, false
	}
	return decodedNodeInfo{
		node:       NodeInfo{NodeID: id.Uint32(), RAAddr: info.RAAddr, StartAt: info.StartAt},
		serverType: info.ServerType,
	}, true
}

// Discover 拉取并 watch 节点
func (r *EtcdRegistry) Discover(onAdd func(NodeInfo, uint32), onDel func(uint32)) error {
	ctx := context.Background()
	resp, err := r.cli.Get(ctx, r.prefix, clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("etcd get: %w", err)
	}
	for _, kv := range resp.Kvs {
		info, ok := decodeNodeInfo(kv.Value)
		if !ok {
			continue
		}
		onAdd(info.node, info.serverType)
	}
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
						info, ok := decodeNodeInfo(ev.Kv.Value)
						if !ok {
							continue
						}
						onAdd(info.node, info.serverType)
					case clientv3.EventTypeDelete:
						id, err := nodeid.Parse(path.Base(string(ev.Kv.Key)))
						if err != nil {
							continue
						}
						onDel(id.Uint32())
					}
				}
			}
		}
	}()
	return nil
}

// PeerAddr 按 nodeID 查询 RA 地址
func (r *EtcdRegistry) PeerAddr(nodeID uint32) (string, bool) {
	resp, err := r.cli.Get(context.Background(), r.nodeKey(nodeID))
	if err != nil || len(resp.Kvs) == 0 {
		return "", false
	}
	info, ok := decodeNodeInfo(resp.Kvs[0].Value)
	if !ok {
		return "", false
	}
	return info.node.RAAddr, true
}

// Close 关闭 etcd 连接
func (r *EtcdRegistry) Close() {
	select {
	case <-r.stopCh:
	default:
		close(r.stopCh)
	}
	_ = r.cli.Close()
}

// EtcdEndpointsFromEnv 读取环境变量
func EtcdEndpointsFromEnv() []string {
	if v := os.Getenv("ETCD_ENDPOINTS"); v != "" {
		return []string{v}
	}
	return nil
}
