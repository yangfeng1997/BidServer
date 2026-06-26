package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"project/internal/server/base"
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
	opts := &base.Options{}

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

func bindCommonFlags(cmd *cobra.Command, opts *base.Options) {
	cmd.PersistentFlags().StringVarP(&opts.PidFile, "pid-file", "p", "gatesvr.pid", "pid file path")
}

func newStartCommand(opts *base.Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start gate server",
		RunE: func(cmd *cobra.Command, args []string) error {
			builder := gate.NewBuilder(gate.Options{
				Options: base.Options{
					Addr:    opts.Addr,
					PidFile: opts.PidFile,
					Daemon:  opts.Daemon,
				},
			})

			if err := base.WritePIDFile(opts.PidFile); err != nil {
				return fmt.Errorf("write pid file: %w", err)
			}
			defer func() {
				if err := base.RemovePIDFile(opts.PidFile); err != nil {
					logger.Error("remove gate pid file failed", logger.Err(err))
				}
			}()

			logger.Info("gatesvr starting",
				logger.String("addr", opts.Addr),
				logger.String("pid_file", opts.PidFile),
				logger.Bool("daemon", opts.Daemon),
			)

			app, err := builder.Build()
			if err != nil {
				return fmt.Errorf("build gate app: %w", err)
			}

			return app.Start()
		},
	}

	cmd.Flags().StringVar(&opts.Addr, "addr", "", "server listen address override")
	cmd.Flags().BoolVar(&opts.Daemon, "daemon", false, "run as daemon")

	return cmd
}

func newStopCommand(opts *base.Options) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop gate server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := base.SignalProcess(opts.PidFile, "stop"); err != nil {
				return fmt.Errorf("stop gatesvr: %w", err)
			}
			fmt.Printf("gatesvr stop pid_file=%s\n", opts.PidFile)
			return nil
		},
	}
}

func newReloadCommand(opts *base.Options) *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Reload gate server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := base.SignalProcess(opts.PidFile, "reload"); err != nil {
				return fmt.Errorf("reload gatesvr: %w", err)
			}
			fmt.Printf("gatesvr reload pid_file=%s\n", opts.PidFile)
			return nil
		},
	}
}
