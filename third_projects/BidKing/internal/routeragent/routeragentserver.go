package routeragent

import (
	loader "project/conf/schema/gen/loader"
	"project/internal/core/app"
	"project/internal/core/runtime"
)

type RouterAgentServer struct{ *app.App }

func Main(opt *Options) error {
	loader.RegisterCommon(opt.ConfigFiles)
	loader.RegisterRouteragent(opt.ConfigFiles)
	s := NewRouterAgentServer(opt)
	if err := s.Init(); err != nil {
		return err
	}
	return s.Run()
}

func NewRouterAgentServer(opt *Options) *RouterAgentServer {
	s := &RouterAgentServer{App: runtime.NewApp(&opt.BaseOptions)}
	s.Register(NewModule())
	return s
}
