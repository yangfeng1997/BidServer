package onlinesvr

import (
	loader "project/conf/schema/gen/loader"
	"project/internal/core/app"
	"project/internal/core/runtime"
)

type OnlineServer struct{ *app.App }

func Main(opt *Options) error {
	loader.RegisterCommon(opt.ConfigFiles)
	loader.RegisterOnlinesvr(opt.ConfigFiles)
	s := NewOnlineServer(opt)
	if err := s.Init(); err != nil {
		return err
	}
	return s.Run()
}

func NewOnlineServer(opt *Options) *OnlineServer {
	s := &OnlineServer{App: runtime.NewApp(&opt.BaseOptions)}
	s.Register(NewModule())
	return s
}
