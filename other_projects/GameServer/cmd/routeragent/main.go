package main

import (
	"os"

	"project/internal/core/app"
	corecmd "project/internal/core/cmd"
	"project/internal/routeragent"
)

func main() {
	cmd := corecmd.Command(
		app.CommandMeta{
			Use:   "routeragent",
			Short: "路由代理（Sidecar）",
			Confs: []string{"run/routeragent/conf/routeragent.yaml", "run/routeragent/conf/routeragent_log.yaml"},
		},
		routeragent.NewOptions,
		nil,
		routeragent.Main,
	)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
