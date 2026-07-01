package main

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"

	opt "project/internal/core/options"
	"project/internal/core/process"
	"project/internal/server/routeragent"
)

func main() {
	if err := execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute() error {
	opts := &routeragent.Options{}
	bindFlags(opts)
	configureUsage("routeragent")
	pflag.Parse()
	if pflag.NArg() > 0 {
		return fmt.Errorf("unexpected args: %v", pflag.Args())
	}

	if opts.CommonConfigPath == "" {
		return fmt.Errorf("common config path is required")
	}
	if opts.RouteragentConfigPath == "" {
		return fmt.Errorf("routeragent config path is required")
	}
	if opts.Daemon {
		started, err := process.StartDaemon()
		if err != nil {
			return fmt.Errorf("start routeragent daemon: %w", err)
		}
		if started {
			return nil
		}
	}

	builder := routeragent.NewRouteragentBuilder(routeragent.Options{
		BaseOptions: opt.BaseOptions{
			PidFile:          opts.PidFile,
			Daemon:           opts.Daemon,
			Pprof:            opts.Pprof,
			PprofAddr:        opts.PprofAddr,
			CommonConfigPath: opts.CommonConfigPath,
		},
		RouteragentConfigPath: opts.RouteragentConfigPath,
	})

	app, err := builder.Build()
	if err != nil {
		return fmt.Errorf("build routeragent app: %w", err)
	}
	if err := process.WritePIDFile(opts.PidFile); err != nil {
		return fmt.Errorf("write routeragent pid file: %w", err)
	}
	defer func() {
		if err := process.RemovePIDFile(opts.PidFile); err != nil {
			fmt.Fprintf(os.Stderr, "remove routeragent pid file: %v\n", err)
		}
	}()

	return app.Startup()
}

func bindFlags(opts *routeragent.Options) {
	pflag.StringVarP(&opts.PidFile, "pid-file", "p", "routeragent.pid", "pid file path")
	pflag.StringVar(&opts.CommonConfigPath, "common-config", "", "common config path")
	pflag.StringVar(&opts.RouteragentConfigPath, "routeragent-config", "", "routeragent config path")
	pflag.BoolVar(&opts.Daemon, "daemon", false, "run as daemon")
	pflag.BoolVar(&opts.Pprof, "pprof", false, "enable pprof server")
	pflag.StringVar(&opts.PprofAddr, "pprof-addr", "127.0.0.1:6060", "pprof listen address")
}

func configureUsage(name string) {
	pflag.CommandLine.SortFlags = false
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "%s starts a sidecar server.\n\n", name)
		fmt.Fprintf(os.Stderr, "Usage:\n  %s [flags]\n\n", name)
		fmt.Fprintln(os.Stderr, "Flags:")
		pflag.PrintDefaults()
	}
}
