package discovery

// Registry 抽象服务发现与注册中心。
// RouterAgent 通过它注册自身和本机业务节点，并监听集群节点变化。
// 默认实现是 EtcdRegistry，未来可替换为 Consul/Nacos/静态配置等。
type Registry interface {
	// Register 注册本节点到注册中心，必须是 CAS：nodeID 已存在时直接报错。
	Register(nodeID uint32, raAddr string, serverType uint32) error

	// PutNode 使用当前 lease 注册业务节点，覆盖写入。
	PutNode(nodeID uint32, raAddr string, serverType uint32) error

	// DeleteNode 删除节点注册。
	DeleteNode(nodeID uint32) error

	// Discover 监听集群节点变化，回调在节点增删时被调用。
	Discover(onPut func(NodeInfo, uint32), onDelete func(uint32)) error

	// Close 关闭注册中心，释放 lease 和 client。
	Close()
}
