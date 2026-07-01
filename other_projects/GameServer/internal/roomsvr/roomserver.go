package roomsvr

import (
	loader "project/conf/schema/gen/loader"
	"project/internal/core/app"
	"project/internal/core/runtime"
)

type RoomServer struct{ *app.App }

func Main(opt *Options) error {
	loader.RegisterCommon(opt.ConfigFiles)
	loader.RegisterRoomsvr(opt.ConfigFiles)
	s := NewRoomServer(opt)
	if err := s.Init(); err != nil {
		return err
	}
	return s.Run()
}

func NewRoomServer(opt *Options) *RoomServer {
	s := &RoomServer{App: runtime.NewApp(&opt.BaseOptions)}
	s.Register(NewModule())
	return s
}
