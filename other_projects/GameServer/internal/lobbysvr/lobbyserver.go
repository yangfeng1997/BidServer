package lobbysvr

import (
	loader "project/conf/schema/gen/loader"
	"project/internal/core/app"
	"project/internal/core/runtime"
)

type LobbyServer struct{ *app.App }

func Main(opt *Options) error {
	loader.RegisterCommon(opt.ConfigFiles)
	loader.RegisterLobbysvr(opt.ConfigFiles)
	s := NewLobbyServer(opt)
	if err := s.Init(); err != nil {
		return err
	}
	return s.Run()
}

func NewLobbyServer(opt *Options) *LobbyServer {
	s := &LobbyServer{App: runtime.NewApp(&opt.BaseOptions)}
	s.Register(NewModule())
	return s
}
