package main

import (
	"os"

	"project/internal/core/app"
	corecmd "project/internal/core/cmd"
	"project/internal/roomsvr"
)

func main() {
	cmd := corecmd.Command(
		app.CommandMeta{
			Use:   "roomsvr",
			Short: "对局房间服务",
			Confs: []string{"run/roomsvr/conf/roomsvr.yaml", "run/roomsvr/conf/roomsvr_log.yaml"},
		},
		roomsvr.NewOptions,
		nil,
		roomsvr.Main,
	)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
