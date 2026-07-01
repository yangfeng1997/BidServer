package main

import (
	"os"

	"project/internal/core/app"
	corecmd "project/internal/core/cmd"
	"project/internal/lobbysvr"
)

func main() {
	cmd := corecmd.Command(
		app.CommandMeta{
			Use:   "lobbysvr",
			Short: "大厅服务",
			Confs: []string{"run/lobbysvr/conf/lobbysvr.yaml", "run/lobbysvr/conf/lobbysvr_log.yaml"},
		},
		lobbysvr.NewOptions,
		nil,
		lobbysvr.Main,
	)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
