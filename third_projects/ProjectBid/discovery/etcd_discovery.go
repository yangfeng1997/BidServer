package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"projectbid/server/cluster"
	"projectbid/server/logger"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// EtcdDiscovery 基于 etcd 的服务发现实现。
type EtcdDiscovery struct {
	cli                 *clientv3.Client
	server              *cluster.Server
	leaseID             clientv3.LeaseID
	heartbeatTTL        time.Duration
	syncServersInterval time.Duration
	etcdPrefix          string

	mu            sync.RWMutex
	serverMapByID   map[string]*cluster.Server
	serverMapByType map[string]map[string]*cluster.Server
	listeners      []SDListener

	stopLeaseChan chan struct{}
	stopSyncChan  chan struct{}
	running       bool
}

// EtcdConfig etcd 服务发现配置。
type EtcdConfig struct {
	Endpoints          []string
	Prefix             string
	HeartbeatTTL       time.Duration
	SyncServersInterval time.Duration
	Username           string
	Password           string
}

// NewEtcdDiscovery 创建 etcd 服务发现实例。
func NewEtcdDiscovery(config EtcdConfig, server *cluster.Server) (*EtcdDiscovery, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   config.Endpoints,
		DialTimeout: 5 * time.Second,
		Username:    config.Username,
		Password:    config.Password,
	})
	if err != nil {
		return nil, fmt.Errorf("连接 etcd 失败: %w", err)
	}

	if config.HeartbeatTTL == 0 {
		config.HeartbeatTTL = 10 * time.Second
	}
	if config.SyncServersInterval == 0 {
		config.SyncServersInterval = 30 * time.Second
	}
	if config.Prefix == "" {
		config.Prefix = "projectbid/"
	}

	return &EtcdDiscovery{
		cli:                 cli,
		server:              server,
		heartbeatTTL:        config.HeartbeatTTL,
		syncServersInterval: config.SyncServersInterval,
		etcdPrefix:          config.Prefix,
		serverMapByID:       make(map[string]*cluster.Server),
		serverMapByType:     make(map[string]map[string]*cluster.Server),
		listeners:           make([]SDListener, 0),
		stopLeaseChan:       make(chan struct{}),
		stopSyncChan:        make(chan struct{}),
	}, nil
}

// Register 注册当前服务并启动心跳。
func (d *EtcdDiscovery) Register(ctx context.Context) error {
	d.running = true

	// 创建租约
	lease, err := d.cli.Grant(ctx, int64(d.heartbeatTTL.Seconds()))
	if err != nil {
		return fmt.Errorf("创建 etcd 租约失败: %w", err)
	}
	d.leaseID = lease.ID

	// 注册服务
	key := fmt.Sprintf("%sservers/%s/%s", d.etcdPrefix, d.server.Type, d.server.ID)
	_, err = d.cli.Put(ctx, key, d.server.AsJSON(), clientv3.WithLease(d.leaseID))
	if err != nil {
		return fmt.Errorf("注册服务到 etcd 失败: %w", err)
	}

	logger.Infow("服务已注册到 etcd",
		"ID", d.server.ID,
		"类型", d.server.Type,
		"Key", key,
	)

	// 启动心跳续约
	go d.keepAlive()

	// 首次同步服务列表
	if err := d.syncServers(true); err != nil {
		logger.Errorw("首次同步服务列表失败", "错误", err)
	}

	// 启动周期性同步
	go d.syncLoop()

	return nil
}

// Deregister 从注册中心注销当前服务。
func (d *EtcdDiscovery) Deregister() error {
	d.running = false
	close(d.stopLeaseChan)
	close(d.stopSyncChan)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if d.leaseID != 0 {
		if _, err := d.cli.Revoke(ctx, d.leaseID); err != nil {
			logger.Errorw("撤销 etcd 租约失败", "错误", err)
		}
	}

	key := fmt.Sprintf("%sservers/%s/%s", d.etcdPrefix, d.server.Type, d.server.ID)
	if _, err := d.cli.Delete(ctx, key); err != nil {
		return fmt.Errorf("从 etcd 删除服务失败: %w", err)
	}

	logger.Infow("服务已从 etcd 注销", "ID", d.server.ID)
	return d.cli.Close()
}

// GetServersByType 按类型获取服务列表。
func (d *EtcdDiscovery) GetServersByType(svType string) map[string]*cluster.Server {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.serverMapByType[svType]
}

// GetServer 按 ID 获取服务。
func (d *EtcdDiscovery) GetServer(id string) (*cluster.Server, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if s, ok := d.serverMapByID[id]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("服务未找到: %s", id)
}

// AddListener 添加服务变更监听器。
func (d *EtcdDiscovery) AddListener(listener SDListener) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.listeners = append(d.listeners, listener)
}

// Stop 停止服务发现。
func (d *EtcdDiscovery) Stop() error {
	if d.running {
		return d.Deregister()
	}
	return d.cli.Close()
}

// ——— 内部方法 ——— //

func (d *EtcdDiscovery) keepAlive() {
	ticker := time.NewTicker(d.heartbeatTTL / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := d.cli.KeepAliveOnce(ctx, d.leaseID)
			cancel()
			if err != nil {
				logger.Errorw("etcd 心跳续约失败", "错误", err)
			}
		case <-d.stopLeaseChan:
			return
		}
	}
}

func (d *EtcdDiscovery) syncLoop() {
	ticker := time.NewTicker(d.syncServersInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := d.syncServers(false); err != nil {
				logger.Errorw("同步服务列表失败", "错误", err)
			}
		case <-d.stopSyncChan:
			return
		}
	}
}

func (d *EtcdDiscovery) syncServers(firstSync bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	prefix := fmt.Sprintf("%sservers/", d.etcdPrefix)
	resp, err := d.cli.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("从 etcd 获取服务列表失败: %w", err)
	}

	newByID := make(map[string]*cluster.Server)
	newByType := make(map[string]map[string]*cluster.Server)

	for _, kv := range resp.Kvs {
		var info cluster.Server
		if err := json.Unmarshal(kv.Value, &info); err != nil {
			logger.Errorw("解析服务信息失败", "错误", err)
			continue
		}
		newByID[info.ID] = &info
		if newByType[info.Type] == nil {
			newByType[info.Type] = make(map[string]*cluster.Server)
		}
		newByType[info.Type][info.ID] = &info
	}

	d.mu.Lock()
	oldByID := d.serverMapByID
	d.serverMapByID = newByID
	d.serverMapByType = newByType
	listeners := make([]SDListener, len(d.listeners))
	copy(listeners, d.listeners)
	d.mu.Unlock()

	// 通知监听器变更
	if !firstSync {
		for id, newServer := range newByID {
			if _, ok := oldByID[id]; !ok {
				for _, l := range listeners {
					l.AddServer(newServer)
				}
			}
		}
		for id, oldServer := range oldByID {
			if _, ok := newByID[id]; !ok {
				for _, l := range listeners {
					l.RemoveServer(oldServer)
				}
			}
		}
	}

	logger.Debugw("服务列表已同步", "服务数量", len(newByID))
	return nil
}
