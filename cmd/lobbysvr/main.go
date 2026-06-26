package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"project/internal/server/base"
	"project/internal/server/lobby"
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
		Use:   "lobbysvr",
		Short: "Lobby server",
	}

	bindCommonFlags(cmd, opts)
	cmd.AddCommand(newStartCommand(opts))
	cmd.AddCommand(newStopCommand(opts))
	cmd.AddCommand(newReloadCommand(opts))

	return cmd
}

func bindCommonFlags(cmd *cobra.Command, opts *base.Options) {
	cmd.PersistentFlags().StringVarP(&opts.PidFile, "pid-file", "p", "lobbysvr.pid", "pid file path")
}

func newStartCommand(opts *base.Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start lobby server",
		RunE: func(cmd *cobra.Command, args []string) error {
			builder := lobby.NewBuilder(lobby.Options{
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
					logger.Error("remove lobby pid file failed", logger.Err(err))
				}
			}()

			logger.Info("lobbysvr starting",
				logger.String("addr", opts.Addr),
				logger.String("pid_file", opts.PidFile),
				logger.Bool("daemon", opts.Daemon),
			)

			app, err := builder.Build()
			if err != nil {
				return fmt.Errorf("build lobby app: %w", err)
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
		Short: "Stop lobby server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := base.SignalProcess(opts.PidFile, "stop"); err != nil {
				return fmt.Errorf("stop lobbysvr: %w", err)
			}
			fmt.Printf("lobbysvr stop pid_file=%s\n", opts.PidFile)
			return nil
		},
	}
}

func newReloadCommand(opts *base.Options) *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Reload lobby server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := base.SignalProcess(opts.PidFile, "reload"); err != nil {
				return fmt.Errorf("reload lobbysvr: %w", err)
			}
			fmt.Printf("lobbysvr reload pid_file=%s\n", opts.PidFile)
			return nil
		},
	}
}
