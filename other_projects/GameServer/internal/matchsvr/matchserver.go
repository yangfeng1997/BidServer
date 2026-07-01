package matchsvr

import (
	loader "project/conf/schema/gen/loader"
	"project/internal/core/app"
	"project/internal/core/runtime"
)

type MatchServer struct{ *app.App }

func Main(opt *Options) error {
	loader.RegisterCommon(opt.ConfigFiles)
	loader.RegisterMatchsvr(opt.ConfigFiles)
	s := NewMatchServer(opt)
	if err := s.Init(); err != nil {
		return err
	}
	return s.Run()
}

func NewMatchServer(opt *Options) *MatchServer {
	s := &MatchServer{App: runtime.NewApp(&opt.BaseOptions)}
	s.Register(NewModule())
	return s
}
