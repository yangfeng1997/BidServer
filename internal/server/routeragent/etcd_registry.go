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
	"project/pkg/logger"

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
	cli              *clientv3.Client
	prefix           string
	lease            clientv3.LeaseID
	nodeID           uint32
	raAddr           string
	stopCh           chan struct{}
	mu               sync.Mutex
	registered       map[uint32]registeredNode
	keepaliveStarted bool
}

type registeredNode struct {
	nodeID     uint32
	raAddr     string
	serverType uint32
	data       string
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
	return &EtcdRegistry{cli: cli, prefix: prefix, stopCh: make(chan struct{}), registered: make(map[uint32]registeredNode)}, nil
}

// Register 注册本节点到 etcd。
// RouterAgent 自身注册必须是 CAS：如果 nodeID 对应 key 已存在，说明有重复节点或旧 lease 未过期，直接报错。
func (r *EtcdRegistry) Register(nodeID uint32, raAddr string, serverType uint32) error {
	r.nodeID = nodeID
	r.raAddr = raAddr
	logger.Info("routeragent etcd register node", logger.String("key", r.nodeKey(nodeID)), logger.String("node_id", nodeid.String(nodeID)), logger.Uint32("server_type", serverType), logger.String("ra_addr", raAddr))
	return r.putNodeIfAbsent(nodeID, raAddr, serverType)
}

// PutNode 使用当前 lease 注册节点到 etcd。
// 业务节点由本机 RouterAgent 代注册，保持覆盖写入，避免业务进程重连时被自身旧值阻塞。
func (r *EtcdRegistry) PutNode(nodeID uint32, raAddr string, serverType uint32) error {
	lease, err := r.ensureLease()
	if err != nil {
		return err
	}
	key := r.nodeKey(nodeID)
	data, err := marshalNodeInfo(nodeID, raAddr, serverType)
	if err != nil {
		return err
	}
	if _, err = r.cli.Put(context.Background(), key, string(data), clientv3.WithLease(lease)); err != nil {
		return fmt.Errorf("etcd put: %w", err)
	}
	r.rememberNode(nodeID, raAddr, serverType, data)
	logger.Info("routeragent etcd put node done", logger.String("key", key), logger.String("node_id", nodeid.String(nodeID)), logger.Uint32("server_type", serverType), logger.String("ra_addr", raAddr), logger.Int64("lease", int64(lease)))
	return nil
}

func (r *EtcdRegistry) putNodeIfAbsent(nodeID uint32, raAddr string, serverType uint32) error {
	lease, err := r.ensureLease()
	if err != nil {
		return err
	}
	key := r.nodeKey(nodeID)
	data, err := marshalNodeInfo(nodeID, raAddr, serverType)
	if err != nil {
		return err
	}
	resp, err := r.cli.Txn(context.Background()).
		If(clientv3.Compare(clientv3.Version(key), "=", 0)).
		Then(clientv3.OpPut(key, string(data), clientv3.WithLease(lease))).
		Else(clientv3.OpGet(key)).
		Commit()
	if err != nil {
		return fmt.Errorf("etcd txn put-if-absent: %w", err)
	}
	if !resp.Succeeded {
		if len(resp.Responses) > 0 && resp.Responses[0].GetResponseRange() != nil {
			for _, kv := range resp.Responses[0].GetResponseRange().Kvs {
				logger.Warn("routeragent etcd register duplicate", logger.String("key", string(kv.Key)), logger.String("value", string(kv.Value)), logger.Int64("lease", kv.Lease), logger.Int64("version", kv.Version))
			}
		}
		return fmt.Errorf("etcd node already exists: key=%s node_id=%s", key, nodeid.String(nodeID))
	}
	logger.Info("routeragent etcd register node done", logger.String("key", key), logger.String("node_id", nodeid.String(nodeID)), logger.Uint32("server_type", serverType), logger.String("ra_addr", raAddr), logger.Int64("lease", int64(lease)), logger.Int64("revision", resp.Header.Revision))
	r.rememberNode(nodeID, raAddr, serverType, data)
	return nil
}

func (r *EtcdRegistry) rememberNode(nodeID uint32, raAddr string, serverType uint32, data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registered[nodeID] = registeredNode{nodeID: nodeID, raAddr: raAddr, serverType: serverType, data: string(data)}
}

func marshalNodeInfo(nodeID uint32, raAddr string, serverType uint32) ([]byte, error) {
	info := nodeInfoJSON{NodeID: nodeid.String(nodeID), ServerType: serverType, RAAddr: raAddr, StartAt: time.Now().Unix()}
	return json.Marshal(info)
}

// DeleteNode 删除节点注册
func (r *EtcdRegistry) DeleteNode(nodeID uint32) error {
	r.mu.Lock()
	delete(r.registered, nodeID)
	r.mu.Unlock()
	_, err := r.cli.Delete(context.Background(), r.nodeKey(nodeID))
	return err
}

func (r *EtcdRegistry) ensureLease() (clientv3.LeaseID, error) {
	r.mu.Lock()
	if r.lease != 0 {
		lease := r.lease
		r.mu.Unlock()
		return lease, nil
	}
	lease, err := r.cli.Grant(context.Background(), 10)
	if err != nil {
		r.mu.Unlock()
		return 0, fmt.Errorf("etcd grant: %w", err)
	}
	r.lease = lease.ID
	startKeepalive := !r.keepaliveStarted
	if startKeepalive {
		r.keepaliveStarted = true
	}
	r.mu.Unlock()

	logger.Info("routeragent etcd lease grant done", logger.Int64("lease", int64(lease.ID)), logger.Int64("ttl", lease.TTL), logger.String("prefix", r.prefix))
	if startKeepalive {
		go r.keepaliveLoop()
	}
	return lease.ID, nil
}

func (r *EtcdRegistry) keepaliveLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := r.keepaliveOnce(); err != nil {
				logger.Warn("routeragent etcd keepalive failed", logger.String("prefix", r.prefix), logger.Err(err))
				if err := r.recoverLease(); err != nil {
					logger.Error("routeragent etcd lease recover failed", logger.String("prefix", r.prefix), logger.Err(err))
				}
			}
		case <-r.stopCh:
			r.mu.Lock()
			lease := r.lease
			r.lease = 0
			r.mu.Unlock()
			if lease != 0 {
				_, _ = r.cli.Revoke(context.Background(), lease)
			}
			return
		}
	}
}

func (r *EtcdRegistry) keepaliveOnce() error {
	r.mu.Lock()
	lease := r.lease
	r.mu.Unlock()
	if lease == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := r.cli.KeepAliveOnce(ctx, lease)
	if err != nil {
		r.mu.Lock()
		if r.lease == lease {
			r.lease = 0
		}
		r.mu.Unlock()
		return fmt.Errorf("etcd keepalive once lease=%d: %w", lease, err)
	}
	if resp == nil {
		r.mu.Lock()
		if r.lease == lease {
			r.lease = 0
		}
		r.mu.Unlock()
		return fmt.Errorf("etcd keepalive once lease=%d nil response", lease)
	}
	return nil
}

func (r *EtcdRegistry) recoverLease() error {
	r.mu.Lock()
	if r.lease != 0 {
		r.mu.Unlock()
		return nil
	}
	lease, err := r.cli.Grant(context.Background(), 10)
	if err != nil {
		r.mu.Unlock()
		return fmt.Errorf("etcd grant: %w", err)
	}
	r.lease = lease.ID
	registered := make([]registeredNode, 0, len(r.registered))
	for _, reg := range r.registered {
		registered = append(registered, reg)
	}
	r.mu.Unlock()

	logger.Info("routeragent etcd lease recover done", logger.Int64("lease", int64(lease.ID)), logger.Int64("ttl", lease.TTL), logger.String("prefix", r.prefix), logger.Int("nodes", len(registered)))
	for _, reg := range registered {
		key := r.nodeKey(reg.nodeID)
		if _, err := r.cli.Put(context.Background(), key, reg.data, clientv3.WithLease(lease.ID)); err != nil {
			r.mu.Lock()
			if r.lease == lease.ID {
				r.lease = 0
			}
			r.mu.Unlock()
			return fmt.Errorf("etcd recover put key=%s node_id=%s: %w", key, nodeid.String(reg.nodeID), err)
		}
		logger.Info("routeragent etcd recover put node done", logger.String("key", key), logger.String("node_id", nodeid.String(reg.nodeID)), logger.Uint32("server_type", reg.serverType), logger.String("ra_addr", reg.raAddr), logger.Int64("lease", int64(lease.ID)))
	}
	return nil
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
	logger.Info("routeragent etcd discover initial get done", logger.String("prefix", r.prefix), logger.Int64("count", resp.Count), logger.Int64("revision", resp.Header.Revision))
	for _, kv := range resp.Kvs {
		info, ok := decodeNodeInfo(kv.Value)
		if !ok {
			logger.Warn("routeragent etcd discover decode skip", logger.String("key", string(kv.Key)), logger.String("value", string(kv.Value)), logger.Int64("lease", kv.Lease), logger.Int64("version", kv.Version))
			continue
		}
		logger.Info("routeragent etcd discover add initial", logger.String("key", string(kv.Key)), logger.String("node_id", nodeid.String(info.node.NodeID)), logger.Uint32("server_type", info.serverType), logger.String("ra_addr", info.node.RAAddr), logger.Int64("lease", kv.Lease), logger.Int64("version", kv.Version))
		onAdd(info.node, info.serverType)
	}
	go func() {
		logger.Info("routeragent etcd watch start", logger.String("prefix", r.prefix))
		watchCh := r.cli.Watch(ctx, r.prefix, clientv3.WithPrefix())
		for {
			select {
			case <-r.stopCh:
				return
			case wresp, ok := <-watchCh:
				if !ok {
					logger.Warn("routeragent etcd watch channel closed", logger.String("prefix", r.prefix))
					return
				}
				if wresp.Err() != nil {
					logger.Error("routeragent etcd watch error", logger.String("prefix", r.prefix), logger.Err(wresp.Err()))
					continue
				}
				logger.Info("routeragent etcd watch response", logger.String("prefix", r.prefix), logger.Int64("revision", wresp.Header.Revision), logger.Int("events", len(wresp.Events)))
				for _, ev := range wresp.Events {
					switch ev.Type {
					case clientv3.EventTypePut:
						info, ok := decodeNodeInfo(ev.Kv.Value)
						if !ok {
							logger.Warn("routeragent etcd watch decode skip", logger.String("key", string(ev.Kv.Key)), logger.String("value", string(ev.Kv.Value)), logger.Int64("lease", ev.Kv.Lease), logger.Int64("version", ev.Kv.Version))
							continue
						}
						logger.Info("routeragent etcd watch put", logger.String("key", string(ev.Kv.Key)), logger.String("node_id", nodeid.String(info.node.NodeID)), logger.Uint32("server_type", info.serverType), logger.String("ra_addr", info.node.RAAddr), logger.Int64("lease", ev.Kv.Lease), logger.Int64("version", ev.Kv.Version))
						onAdd(info.node, info.serverType)
					case clientv3.EventTypeDelete:
						id, err := nodeid.Parse(path.Base(string(ev.Kv.Key)))
						if err != nil {
							logger.Warn("routeragent etcd watch delete parse skip", logger.String("key", string(ev.Kv.Key)), logger.Err(err))
							continue
						}
						logger.Info("routeragent etcd watch delete", logger.String("key", string(ev.Kv.Key)), logger.String("node_id", id.String()))
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
