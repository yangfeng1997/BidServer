package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	opt "project/internal/core/options"
	"project/internal/core/process"
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
	opts := &lobby.Options{}

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

func bindCommonFlags(cmd *cobra.Command, opts *lobby.Options) {
	cmd.PersistentFlags().StringVarP(&opts.PidFile, "pid-file", "p", "lobbysvr.pid", "pid file path")
}

func newStartCommand(opts *lobby.Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start lobby server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.CommonConfigPath == "" {
				return fmt.Errorf("common config path is required")
			}
			if opts.LobbyConfigPath == "" {
				return fmt.Errorf("lobby config path is required")
			}

			builder := lobby.NewLobbyBuilder(lobby.Options{
				BaseOptions: opt.BaseOptions{
					PidFile:          opts.PidFile,
					Daemon:           opts.Daemon,
					CommonConfigPath: opts.CommonConfigPath,
				},
				LobbyConfigPath: opts.LobbyConfigPath,
			})

			if err := process.WritePIDFile(opts.PidFile); err != nil {
				return fmt.Errorf("write pid file: %w", err)
			}
			defer func() {
				if err := process.RemovePIDFile(opts.PidFile); err != nil {
					logger.Error("remove lobby pid file failed", logger.Err(err))
				}
			}()

			app, err := builder.Build()
			if err != nil {
				return fmt.Errorf("build lobby app: %w", err)
			}

			return app.Start()
		},
	}

	cmd.Flags().StringVar(&opts.CommonConfigPath, "common-config", "", "common config path")
	cmd.Flags().StringVar(&opts.LobbyConfigPath, "lobby-config", "", "lobby config path")
	cmd.Flags().BoolVar(&opts.Daemon, "daemon", false, "run as daemon")

	return cmd
}

func newStopCommand(opts *lobby.Options) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop lobby server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := process.SignalProcess(opts.PidFile, "stop"); err != nil {
				return fmt.Errorf("stop lobbysvr: %w", err)
			}
			fmt.Printf("lobbysvr stop pid_file=%s\n", opts.PidFile)
			return nil
		},
	}
}

func newReloadCommand(opts *lobby.Options) *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Reload lobby server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := process.SignalProcess(opts.PidFile, "reload"); err != nil {
				return fmt.Errorf("reload lobbysvr: %w", err)
			}
			fmt.Printf("lobbysvr reload pid_file=%s\n", opts.PidFile)
			return nil
		},
	}
}
