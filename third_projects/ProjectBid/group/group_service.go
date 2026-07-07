package group

// GroupService 定义全局分组管理接口。
type GroupService interface {
	// NewGroup 创建新分组。
	NewGroup(name string) *Group

	// GetGroup 获取指定名称的分组，不存在返回 nil。
	GetGroup(name string) *Group

	// RemoveGroup 移除并关闭分组，返回 true 表示成功移除。
	RemoveGroup(name string) bool

	// Count 返回当前分组总数。
	Count() int
}

// NewGroupService 创建基于内存的分组服务。
func NewGroupService() GroupService {
	return newGroupServiceImpl()
}
