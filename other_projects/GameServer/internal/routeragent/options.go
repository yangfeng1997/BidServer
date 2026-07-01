package routeragent

import "project/internal/core/app"

type Options struct{ app.BaseOptions }

func NewOptions() *Options {
	opt := &Options{}
	opt.Defaults()
	return opt
}

func (o *Options) Base() *app.BaseOptions { return &o.BaseOptions }
func (o *Options) Defaults()              { o.BaseOptions.Defaults() }
