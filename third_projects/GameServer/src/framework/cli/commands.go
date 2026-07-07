package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"project/src/common/daemon"
	"project/src/common/pidfile"
)

func newStartCmd(f *Flags, onStart func(*Flags) error) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "启动服务",
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.Daemon {
				return daemon.Daemonize()
			}
			if f.PidFile != "" {
				if pidfile.IsRunning(f.PidFile) {
					return fmt.Errorf("service already running (pid-file: %s)", f.PidFile)
				}
				if err := pidfile.TryWrite(f.PidFile, os.Getpid()); err != nil {
					if os.IsExist(err) {
						return fmt.Errorf("service already running (pid-file exists: %s)", f.PidFile)
					}
					return fmt.Errorf("write pid-file: %w", err)
				}
				defer pidfile.Remove(f.PidFile)
			}
			if onStart == nil {
				return fmt.Errorf("OnStart callback not registered")
			}
			return onStart(f)
		},
	}
}

func newStopCmd(f *Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "优雅停止服务（SIGTERM）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return sendSignalTERM(f.PidFile)
		},
	}
}

func newKillCmd(f *Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "kill",
		Short: "强制杀死服务（SIGKILL）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return sendSignalKILL(f.PidFile)
		},
	}
}

func newReloadCmd(f *Flags, onReload func(*Flags) error) *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "重载配置（SIGHUP）",
		RunE: func(cmd *cobra.Command, args []string) error {
			if onReload != nil {
				return onReload(f)
			}
			return sendSignalHUP(f.PidFile)
		},
	}
}

func newStatusCmd(f *Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "查看服务运行状态",
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.PidFile == "" {
				return fmt.Errorf("--pid-file not specified")
			}
			if pidfile.IsRunning(f.PidFile) {
				pid, _ := pidfile.Read(f.PidFile)
				fmt.Printf("running (pid=%d)\n", pid)
			} else {
				fmt.Println("stopped")
			}
			return nil
		},
	}
}

func newVersionCmd(name, revision string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印版本信息",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("%s %s\n", name, revision)
		},
	}
}

// readPID 从 pid-file 读取 PID，做基础校验。
func readPID(pidFile string) (int, error) {
	if pidFile == "" {
		return 0, fmt.Errorf("--pid-file not specified")
	}
	pid, err := pidfile.Read(pidFile)
	if err != nil {
		return 0, fmt.Errorf("read pid-file: %w", err)
	}
	return pid, nil
}
