package cli

import "github.com/spf13/cobra"

// Flags 是所有服务共用的 CLI 参数集合。
type Flags struct {
	Addr     string
	ConfFile string
	PidFile  string
	LogFile  string
	Daemon   bool
}

func bindPersistentFlags(cmd *cobra.Command, f *Flags, defaultConf string) {
	pf := cmd.PersistentFlags()
	pf.StringVarP(&f.Addr, "addr", "a", "", "监听地址（覆盖配置文件）")
	pf.StringVarP(&f.ConfFile, "conf-file", "c", defaultConf, "服务配置文件路径")
	pf.StringVarP(&f.PidFile, "pid-file", "p", "", "PID 文件路径")
	pf.StringVarP(&f.LogFile, "log-file", "l", "", "日志配置文件路径")
	pf.BoolVarP(&f.Daemon, "daemon", "d", false, "以后台方式运行（仅 start 子命令有效）")
}
