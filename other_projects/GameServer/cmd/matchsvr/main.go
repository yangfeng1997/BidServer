package main

import (
	"os"

	"project/internal/core/app"
	corecmd "project/internal/core/cmd"
	"project/internal/matchsvr"
)

func main() {
	cmd := corecmd.Command(
		app.CommandMeta{
			Use:   "matchsvr",
			Short: "匹配服务",
			Confs: []string{"run/matchsvr/conf/matchsvr.yaml", "run/matchsvr/conf/matchsvr_log.yaml"},
		},
		matchsvr.NewOptions,
		nil,
		matchsvr.Main,
	)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
