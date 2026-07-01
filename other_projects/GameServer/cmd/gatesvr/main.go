package main

import (
	"os"

	"github.com/spf13/cobra"

	"project/internal/core/app"
	corecmd "project/internal/core/cmd"
	"project/internal/gatesvr"
)

func main() {
	cmd := corecmd.Command(
		app.CommandMeta{
			Use:   "gatesvr",
			Short: "游戏网关服务",
			Confs: []string{"run/common/conf/", "run/gatesvr/conf/"},
		},
		gatesvr.NewOptions,
		bindFlags,
		gatesvr.Main,
	)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func bindFlags(cmd *cobra.Command, opt *gatesvr.Options) {
	cmd.Flags().StringVar(&opt.ListenAddr, "listen-addr", opt.ListenAddr, "监听地址")
}
