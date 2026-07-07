package discovery

import (
	"context"
	"fmt"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"project/src/common/logger"
	"project/src/framework/cluster"
	"project/src/framework/cluster/pb"
)

const (
	etcdLeaseTTL     = 10
	etcdDialTimeout  = 5 * time.Second
	etcdSyncInterval = 30 * time.Second
	etcdMaxRetries   = 10
	etcdKeyPrefix    = "nodes/"
	shutdownDelay    = 300 * time.Millisecond
)

// NodeInfo 使用 pb.NodeInfo（protobuf 生成）
type NodeInfo = pb.NodeInfo

// etcdKey 生成 etcd key：nodes/world-{worldID}/{serverTypeName}/{nodeID}
func etcdKey(worldID uint16, serverTypeName string, nodeID cluster.NodeID) string {
	return fmt.Sprintf("%sworld-%d/%s/%s", etcdKeyPrefix, worldID, serverTypeName, nodeID.String())
}

// SDListener 节点上下线回调接口
type SDListener interface {
	OnNodeAdd(info *NodeInfo)
	OnNodeRemove(nodeID string)
}

// Discovery etcd 服务注册与发现
type Discovery struct {
	cli      *clientv3.Client
	leaseID  clientv3.LeaseID
	selfKey  string
	selfInfo *NodeInfo

	mu     sync.RWMutex
	byID   map[string]*NodeInfo
	byType map[string]map[string]*NodeInfo

	listeners []SDListener
	stopCh    chan struct{}
	dieCh     chan struct{}
}

// NewDiscovery 创建 Discovery
func NewDiscovery(endpoints []string, self cluster.NodeID, serverTypeName string, selfAddr string, dieCh chan struct{}) (*Discovery, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: etcdDialTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("etcd connect failed: %w", err)
	}
	info := &NodeInfo{
		NodeId:         self.String(),
		ServerTypeName: serverTypeName,
		Subject:        self.Subject(),
		Addr:           selfAddr,
		StartTime:      time.Now().Unix(),
	}
	key := etcdKey(self.WorldID(), serverTypeName, self)
	d := &Discovery{
		cli:      cli,
		selfKey:  key,
		selfInfo: info,
		byID:     make(map[string]*NodeInfo),
		byType:   make(map[string]map[string]*NodeInfo),
		stopCh:   make(chan struct{}),
		dieCh:    dieCh,
	}
	return d, nil
}

// AddListener 注册节点上下线监听器
func (d *Discovery) AddListener(l SDListener) {
	d.listeners = append(d.listeners, l)
}

// Init 注册本节点，加载全量节点，启动 Watch
func (d *Discovery) Init() error {
	if err := d.grantLease(); err != nil {
		return err
	}
	if err := d.register(); err != nil {
		return err
	}
	if err := d.syncAll(); err != nil {
		return err
	}
	go d.watchEtcd()
	go d.syncLoop()
	return nil
}

// Stop 主动 Revoke Lease，等待 shutdownDelay 让其他节点感知后关闭
func (d *Discovery) Stop() error {
	close(d.stopCh)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := d.cli.Revoke(ctx, d.leaseID)
	time.Sleep(shutdownDelay)
	d.cli.Close()
	return err
}

// ByID 按 NodeID 字符串查询节点信息
func (d *Discovery) ByID(nodeID string) (*NodeInfo, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	n, ok := d.byID[nodeID]
	return n, ok
}

// ByType 按服务类型名称查询该类型所有节点
func (d *Discovery) ByType(serverTypeName string) []*NodeInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	m := d.byType[serverTypeName]
	nodes := make([]*NodeInfo, 0, len(m))
	for _, n := range m {
		nodes = append(nodes, n)
	}
	return nodes
}

// Dump 返回当前所有节点的可读 JSON 字符串，供运维查看
// 使用 protojson 把 protobuf 消息转为标准 JSON，字段名与 proto 定义一致
func (d *Discovery) Dump() string {
	d.mu.RLock()
	nodes := make([]*NodeInfo, 0, len(d.byID))
	for _, n := range d.byID {
		nodes = append(nodes, n)
	}
	d.mu.RUnlock()

	opts := protojson.MarshalOptions{
		Multiline:       true,
		EmitUnpopulated: false,
	}
	result := "[\n"
	for i, n := range nodes {
		b, err := opts.Marshal(n)
		if err != nil {
			result += fmt.Sprintf(`{"error": "marshal failed: %v"}`, err)
		} else {
			result += string(b)
		}
		if i < len(nodes)-1 {
			result += ","
		}
		result += "\n"
	}
	result += "]"
	return result
}

// DumpNode 返回单个节点的可读 JSON 字符串
func (d *Discovery) DumpNode(nodeID string) string {
	n, ok := d.ByID(nodeID)
	if !ok {
		return fmt.Sprintf(`{"error": "node %s not found"}`, nodeID)
	}
	b, err := protojson.MarshalOptions{Multiline: true}.Marshal(n)
	if err != nil {
		return fmt.Sprintf(`{"error": "marshal failed: %v"}`, err)
	}
	return string(b)
}

func (d *Discovery) grantLease() error {
	ctx, cancel := context.WithTimeout(context.Background(), etcdDialTimeout)
	defer cancel()
	resp, err := d.cli.Grant(ctx, etcdLeaseTTL)
	if err != nil {
		return fmt.Errorf("etcd grant lease failed: %w", err)
	}
	d.leaseID = resp.ID
	ch, err := d.cli.KeepAlive(context.Background(), d.leaseID)
	if err != nil {
		return fmt.Errorf("etcd keepalive failed: %w", err)
	}
	go d.watchLease(ch)
	return nil
}

func (d *Discovery) watchLease(ch <-chan *clientv3.LeaseKeepAliveResponse) {
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				logger.Warn("etcd lease keepalive lost, renewing")
				if err := d.grantLease(); err != nil {
					logger.Error("etcd renew lease failed, signaling die", logger.Err(err))
					select {
					case d.dieCh <- struct{}{}:
					default:
					}
				}
				return
			}
		case <-d.stopCh:
			return
		}
	}
}

func (d *Discovery) register() error {
	val, err := proto.Marshal(d.selfInfo)
	if err != nil {
		return fmt.Errorf("marshal nodeinfo failed: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), etcdDialTimeout)
	defer cancel()
	_, err = d.cli.Put(ctx, d.selfKey, string(val), clientv3.WithLease(d.leaseID))
	if err != nil {
		return fmt.Errorf("etcd register failed: %w", err)
	}
	logger.Info("registered to etcd", logger.String("key", d.selfKey))
	return nil
}

func (d *Discovery) syncAll() error {
	ctx, cancel := context.WithTimeout(context.Background(), etcdDialTimeout)
	defer cancel()
	resp, err := d.cli.Get(ctx, etcdKeyPrefix, clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("etcd sync failed: %w", err)
	}
	d.mu.Lock()
	for _, kv := range resp.Kvs {
		var info pb.NodeInfo
		if err := proto.Unmarshal(kv.Value, &info); err != nil || info.NodeId == "" {
			continue
		}
		d.addLocked(&info)
	}
	d.mu.Unlock()
	return nil
}

func (d *Discovery) watchEtcd() {
	retries := 0
	for {
		select {
		case <-d.stopCh:
			return
		default:
		}
		ch := d.cli.Watch(context.Background(), etcdKeyPrefix, clientv3.WithPrefix())
		retries = 0
		for wresp := range ch {
			if wresp.Err() != nil {
				retries++
				logger.Warn("etcd watch error", logger.Err(wresp.Err()))
				if retries >= etcdMaxRetries {
					logger.Error("etcd watch max retries exceeded, signaling die")
					select {
					case d.dieCh <- struct{}{}:
					default:
					}
					return
				}
				break
			}
			for _, ev := range wresp.Events {
				switch ev.Type {
				case clientv3.EventTypePut:
					var info pb.NodeInfo
					if err := proto.Unmarshal(ev.Kv.Value, &info); err == nil && info.NodeId != "" {
						d.mu.Lock()
						d.addLocked(&info)
						d.mu.Unlock()
						d.notifyAdd(&info)
					}
				case clientv3.EventTypeDelete:
					nodeID := extractNodeID(string(ev.Kv.Key))
					d.mu.Lock()
					d.removeLocked(nodeID)
					d.mu.Unlock()
					d.notifyRemove(nodeID)
				}
			}
		}
		select {
		case <-d.stopCh:
			return
		case <-time.After(time.Second):
		}
	}
}

func (d *Discovery) syncLoop() {
	ticker := time.NewTicker(etcdSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := d.syncAll(); err != nil {
				logger.Warn("etcd periodic sync failed", logger.Err(err))
			}
		case <-d.stopCh:
			return
		}
	}
}

func (d *Discovery) addLocked(info *NodeInfo) {
	d.byID[info.NodeId] = info
	if d.byType[info.ServerTypeName] == nil {
		d.byType[info.ServerTypeName] = make(map[string]*NodeInfo)
	}
	d.byType[info.ServerTypeName][info.NodeId] = info
}

func (d *Discovery) removeLocked(nodeID string) {
	if info, ok := d.byID[nodeID]; ok {
		if m := d.byType[info.ServerTypeName]; m != nil {
			delete(m, nodeID)
			if len(m) == 0 {
				delete(d.byType, info.ServerTypeName)
			}
		}
		delete(d.byID, nodeID)
	}
}

func extractNodeID(key string) string {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '/' {
			return key[i+1:]
		}
	}
	return key
}

func (d *Discovery) notifyAdd(info *NodeInfo) {
	for _, l := range d.listeners {
		l.OnNodeAdd(info)
	}
}

func (d *Discovery) notifyRemove(nodeID string) {
	for _, l := range d.listeners {
		l.OnNodeRemove(nodeID)
	}
}
