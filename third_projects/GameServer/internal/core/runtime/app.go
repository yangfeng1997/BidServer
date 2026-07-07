// Package runtime 将 app 生命周期容器与 config、log、ragent 组装成可用的 App
package runtime

import (
	"project/internal/core/app"
	"project/internal/core/config"
	corelog "project/internal/core/log"
	"project/internal/core/ragent"
)

// NewApp 创建 App 并注册 config、log、ragent 基础设施模块
// opt 为 nil 时创建 BaseOptions 并调用 Defaults()
func NewApp(opt *app.BaseOptions) *app.App {
	if opt == nil {
		opt = &app.BaseOptions{}
		opt.Defaults()
	}
	a := app.New(opt)
	a.RegisterInfra(config.NewModule())
	a.RegisterInfra(corelog.NewModule())
	a.RegisterInfra(ragent.NewModule())
	return a
}
