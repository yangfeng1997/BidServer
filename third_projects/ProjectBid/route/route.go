// Package route 定义消息路由的解析与格式化。
package route

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrRouteFieldCantEmpty = errors.New("路由字段不可为空")
	ErrInvalidRoute        = errors.New("路由格式无效")
)

// Route 表示一条消息路由，格式为 [ServerType.]Service.Method。
type Route struct {
	SvType  string // 目标服务器类型，空表示本地服务
	Service string // 服务名称
	Method  string // 方法名称
}

// NewRoute 创建路由。
func NewRoute(svType, service, method string) *Route {
	return &Route{SvType: svType, Service: service, Method: method}
}

// String 返回完整路由字符串。
func (r *Route) String() string {
	if r.SvType != "" {
		return fmt.Sprintf("%s.%s.%s", r.SvType, r.Service, r.Method)
	}
	return r.Short()
}

// Short 返回短路由字符串（Service.Method）。
func (r *Route) Short() string {
	return fmt.Sprintf("%s.%s", r.Service, r.Method)
}

// Decode 解析路由字符串。
// 支持格式："ServerType.Service.Method" 和 "Service.Method"
func Decode(routeStr string) (*Route, error) {
	if routeStr == "" {
		return nil, ErrRouteFieldCantEmpty
	}

	parts := strings.Split(routeStr, ".")
	for _, p := range parts {
		if p == "" {
			return nil, ErrRouteFieldCantEmpty
		}
	}

	switch len(parts) {
	case 2:
		return &Route{Service: parts[0], Method: parts[1]}, nil
	case 3:
		return &Route{SvType: parts[0], Service: parts[1], Method: parts[2]}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrInvalidRoute, routeStr)
	}
}
