package gatesvr

import (
	loader "project/conf/schema/gen/loader"
	"project/internal/core/app"
	"project/internal/core/runtime"
)

type GateServer struct{ *app.App }

func Main(opt *Options) error {
	loader.RegisterCommon(opt.ConfigFiles)
	loader.RegisterGatesvr(opt.ConfigFiles)
	s := NewGateServer(opt)
	if err := s.Init(); err != nil {
		return err
	}
	return s.Run()
}

func NewGateServer(opt *Options) *GateServer {
	s := &GateServer{App: runtime.NewApp(&opt.BaseOptions)}
	s.Register(NewModule(opt.ListenAddr))
	return s
}
