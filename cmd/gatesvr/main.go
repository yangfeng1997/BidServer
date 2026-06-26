package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	opt "project/internal/core/options"
	"project/internal/core/process"
	"project/internal/server/gate"
	"project/pkg/logger"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	opts := &gate.Options{}

	cmd := &cobra.Command{
		Use:   "gatesvr",
		Short: "Gate server",
	}

	bindCommonFlags(cmd, opts)
	cmd.AddCommand(newStartCommand(opts))
	cmd.AddCommand(newStopCommand(opts))
	cmd.AddCommand(newReloadCommand(opts))

	return cmd
}

func bindCommonFlags(cmd *cobra.Command, opts *gate.Options) {
	cmd.PersistentFlags().StringVarP(&opts.PidFile, "pid-file", "p", "gatesvr.pid", "pid file path")
}

func newStartCommand(opts *gate.Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start gate server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.CommonConfigPath == "" {
				return fmt.Errorf("common config path is required")
			}
			if opts.GateConfigPath == "" {
				return fmt.Errorf("gate config path is required")
			}

			builder := gate.NewGateBuilder(gate.Options{
				BaseOptions: opt.BaseOptions{
					PidFile:          opts.PidFile,
					Daemon:           opts.Daemon,
					CommonConfigPath: opts.CommonConfigPath,
				},
				GateConfigPath: opts.GateConfigPath,
			})

			if err := process.WritePIDFile(opts.PidFile); err != nil {
				return fmt.Errorf("write pid file: %w", err)
			}
			defer func() {
				if err := process.RemovePIDFile(opts.PidFile); err != nil {
					logger.Error("remove gate pid file failed", logger.Err(err))
				}
			}()

			app, err := builder.Build()
			if err != nil {
				return fmt.Errorf("build gate app: %w", err)
			}

			return app.Start()
		},
	}

	cmd.Flags().StringVar(&opts.CommonConfigPath, "common-config", "", "common config path")
	cmd.Flags().StringVar(&opts.GateConfigPath, "gate-config", "", "gate config path")
	cmd.Flags().BoolVar(&opts.Daemon, "daemon", false, "run as daemon")

	return cmd
}

func newStopCommand(opts *gate.Options) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop gate server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := process.SignalProcess(opts.PidFile, "stop"); err != nil {
				return fmt.Errorf("stop gatesvr: %w", err)
			}
			fmt.Printf("gatesvr stop pid_file=%s\n", opts.PidFile)
			return nil
		},
	}
}

func newReloadCommand(opts *gate.Options) *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Reload gate server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := process.SignalProcess(opts.PidFile, "reload"); err != nil {
				return fmt.Errorf("reload gatesvr: %w", err)
			}
			fmt.Printf("gatesvr reload pid_file=%s\n", opts.PidFile)
			return nil
		},
	}
}
