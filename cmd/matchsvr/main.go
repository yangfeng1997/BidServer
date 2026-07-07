package main

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"

	"project/internal/core/nodeid"
	opt "project/internal/core/options"
	"project/internal/core/process"
	"project/internal/server/match"
)

func main() {
	if err := execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute() error {
	opts := &match.Options{}
	bindFlags(opts)
	configureUsage("matchsvr")
	pflag.Parse()
	if pflag.NArg() > 0 {
		return fmt.Errorf("unexpected args: %v", pflag.Args())
	}

	if opts.CommonConfigPath == "" {
		return fmt.Errorf("common config path is required")
	}
	if opts.MatchConfigPath == "" {
		return fmt.Errorf("match config path is required")
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
			return fmt.Errorf("start matchsvr daemon: %w", err)
		}
		if started {
			return nil
		}
	}

	builder := match.NewMatchBuilder(match.Options{
		BaseOptions: opt.BaseOptions{
			PidFile:          opts.PidFile,
			Daemon:           opts.Daemon,
			Pprof:            opts.Pprof,
			PprofAddr:        opts.PprofAddr,
			CommonConfigPath: opts.CommonConfigPath,
			NodeID:           opts.NodeID,
		},
		MatchConfigPath: opts.MatchConfigPath,
	})

	app, err := builder.Build()
	if err != nil {
		return fmt.Errorf("build matchsvr app: %w", err)
	}
	if err := process.WritePIDFile(opts.PidFile); err != nil {
		return fmt.Errorf("write matchsvr pid file: %w", err)
	}
	defer func() {
		if err := process.RemovePIDFile(opts.PidFile); err != nil {
			fmt.Fprintf(os.Stderr, "remove matchsvr pid file: %v\n", err)
		}
	}()

	return app.Startup()
}

func bindFlags(opts *match.Options) {
	pflag.StringVarP(&opts.PidFile, "pid-file", "p", "matchsvr.pid", "pid file path")
	pflag.StringVar(&opts.NodeID, "nodeid", "", "node id in world.serverType.index format")
	pflag.StringVar(&opts.CommonConfigPath, "common-config", "", "common config path")
	pflag.StringVar(&opts.MatchConfigPath, "match-config", "", "match config path")
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
