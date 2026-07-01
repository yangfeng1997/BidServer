package gatesvr

import (
	"time"

	"project/internal/core/app"
)

type Options struct {
	app.BaseOptions
	ListenAddr string
}

func NewOptions() *Options {
	opt := &Options{}
	opt.Defaults()
	return opt
}

func (o *Options) Base() *app.BaseOptions { return &o.BaseOptions }
func (o *Options) Defaults() {
	o.BaseOptions.Defaults()
	o.Tick = 100 * time.Millisecond
	o.ListenAddr = "0.0.0.0:7001"
}
