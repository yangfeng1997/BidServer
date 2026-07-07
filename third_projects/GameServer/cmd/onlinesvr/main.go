package main

import (
	"os"

	"project/internal/core/app"
	corecmd "project/internal/core/cmd"
	"project/internal/onlinesvr"
)

func main() {
	cmd := corecmd.Command(
		app.CommandMeta{
			Use:   "onlinesvr",
			Short: "在线状态服务",
			Confs: []string{"run/onlinesvr/conf/onlinesvr.yaml", "run/onlinesvr/conf/onlinesvr_log.yaml"},
		},
		onlinesvr.NewOptions,
		nil,
		onlinesvr.Main,
	)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
