package router

import "errors"

var (
	errServiceDiscoveryNotSet = errors.New("服务发现未设置")
	errNoServersAvailable     = errors.New("没有可用的服务器")
)
