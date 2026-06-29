package main

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"

	opt "project/internal/core/options"
	"project/internal/core/process"
	"project/internal/server/gate"
)

func main() {
	if err := execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute() error {
	opts := &gate.Options{}
	bindFlags(opts)
	pflag.Parse()
	if pflag.NArg() > 0 {
		return fmt.Errorf("unexpected args: %v", pflag.Args())
	}

	if opts.CommonConfigPath == "" {
		return fmt.Errorf("common config path is required")
	}
	if opts.GateConfigPath == "" {
		return fmt.Errorf("gate config path is required")
	}
	if opts.Daemon {
		started, err := process.StartDaemon()
		if err != nil {
			return fmt.Errorf("start gate daemon: %w", err)
		}
		if started {
			return nil
		}
	}

	builder := gate.NewGateBuilder(gate.Options{
		BaseOptions: opt.BaseOptions{
			PidFile:          opts.PidFile,
			Daemon:           opts.Daemon,
			Pprof:            opts.Pprof,
			PprofAddr:        opts.PprofAddr,
			CommonConfigPath: opts.CommonConfigPath,
		},
		GateConfigPath: opts.GateConfigPath,
	})

	app, err := builder.Build()
	if err != nil {
		return fmt.Errorf("build gate app: %w", err)
	}
	if err := process.WritePIDFile(opts.PidFile); err != nil {
		return fmt.Errorf("write gate pid file: %w", err)
	}
	defer func() {
		if err := process.RemovePIDFile(opts.PidFile); err != nil {
			fmt.Fprintf(os.Stderr, "remove gate pid file: %v\n", err)
		}
	}()

	return app.Startup()
}

func bindFlags(opts *gate.Options) {
	pflag.StringVarP(&opts.PidFile, "pid-file", "p", "gatesvr.pid", "pid file path")
	pflag.StringVar(&opts.CommonConfigPath, "common-config", "", "common config path")
	pflag.StringVar(&opts.GateConfigPath, "gate-config", "", "gate config path")
	pflag.BoolVar(&opts.Daemon, "daemon", false, "run as daemon")
	pflag.BoolVar(&opts.Pprof, "pprof", false, "enable pprof server")
	pflag.StringVar(&opts.PprofAddr, "pprof-addr", "127.0.0.1:6060", "pprof listen address")
}
