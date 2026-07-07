package main

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"

	"project/internal/core/nodeid"
	opt "project/internal/core/options"
	"project/internal/core/process"
	"project/internal/server/online"
)

func main() {
	if err := execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute() error {
	opts := &online.Options{}
	bindFlags(opts)
	configureUsage("onlinesvr")
	pflag.Parse()
	if pflag.NArg() > 0 {
		return fmt.Errorf("unexpected args: %v", pflag.Args())
	}

	if opts.CommonConfigPath == "" {
		return fmt.Errorf("common config path is required")
	}
	if opts.OnlineConfigPath == "" {
		return fmt.Errorf("online config path is required")
	}
	if opts.NodeID == "" {
		return fmt.Errorf("nodeid is required")
	}
	if _, err := nodeid.Parse(opts.NodeID); err != nil {
		return err
	}
	if opts.Daemon {
		started, err := process.StartDaemon()
		if err != nil {
			return fmt.Errorf("start onlinesvr daemon: %w", err)
		}
		if started {
			return nil
		}
	}

	builder := online.NewOnlineBuilder(online.Options{
		BaseOptions: opt.BaseOptions{
			PidFile:          opts.PidFile,
			Daemon:           opts.Daemon,
			Pprof:            opts.Pprof,
			PprofAddr:        opts.PprofAddr,
			CommonConfigPath: opts.CommonConfigPath,
			NodeID:           opts.NodeID,
		},
		OnlineConfigPath: opts.OnlineConfigPath,
	})

	app, err := builder.Build()
	if err != nil {
		return fmt.Errorf("build onlinesvr app: %w", err)
	}
	if err := process.WritePIDFile(opts.PidFile); err != nil {
		return fmt.Errorf("write onlinesvr pid file: %w", err)
	}
	defer func() {
		if err := process.RemovePIDFile(opts.PidFile); err != nil {
			fmt.Fprintf(os.Stderr, "remove onlinesvr pid file: %v\n", err)
		}
	}()

	return app.Startup()
}

func bindFlags(opts *online.Options) {
	pflag.StringVarP(&opts.PidFile, "pid-file", "p", "onlinesvr.pid", "pid file path")
	pflag.StringVar(&opts.NodeID, "nodeid", "", "node id in world.serverType.index format")
	pflag.StringVar(&opts.CommonConfigPath, "common-config", "", "common config path")
	pflag.StringVar(&opts.OnlineConfigPath, "online-config", "", "online config path")
	pflag.BoolVar(&opts.Daemon, "daemon", false, "run as daemon")
	pflag.BoolVar(&opts.Pprof, "pprof", false, "enable pprof server")
	pflag.StringVar(&opts.PprofAddr, "pprof-addr", "127.0.0.1:6060", "pprof listen address")
}

func configureUsage(name string) {
	pflag.CommandLine.SortFlags = false
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "%s starts a standard server.\n\n", name)
		fmt.Fprintf(os.Stderr, "Usage:\n  %s [flags]\n\n", name)
		fmt.Fprintln(os.Stderr, "Flags:")
		pflag.PrintDefaults()
	}
}
