package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Builder 用于组装服务 CLI 入口。
type Builder struct {
	name        string
	short       string
	defaultConf string
	gitRevision string
	onStart     func(*Flags) error
	onReload    func(*Flags) error
}

// New 创建 Builder。name 为二进制名称，short 为一行描述。
func New(name, short string) *Builder {
	return &Builder{name: name, short: short}
}

// DefaultConf 设置 --conf-file 的默认值。
func (b *Builder) DefaultConf(path string) *Builder {
	b.defaultConf = path
	return b
}

// GitRevision 注入编译期 git 版本号（通过 -ldflags "-X main.GitRevision=xxx"）。
func (b *Builder) GitRevision(rev string) *Builder {
	b.gitRevision = rev
	return b
}

// OnStart 注册 start 子命令的业务逻辑回调。
func (b *Builder) OnStart(fn func(*Flags) error) *Builder {
	b.onStart = fn
	return b
}

// OnReload 注册 reload 子命令的回调。
// 若不注册，reload 子命令默认发送 SIGHUP。
func (b *Builder) OnReload(fn func(*Flags) error) *Builder {
	b.onReload = fn
	return b
}

// Execute 组装 cobra 根命令并执行，遇到错误输出到 stderr 后以 exit(1) 退出。
func (b *Builder) Execute() {
	var f Flags

	root := &cobra.Command{
		Use:           b.name,
		Short:         b.short,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	bindPersistentFlags(root, &f, b.defaultConf)

	root.AddCommand(
		newStartCmd(&f, b.onStart),
		newStopCmd(&f),
		newKillCmd(&f),
		newReloadCmd(&f, b.onReload),
		newStatusCmd(&f),
		newVersionCmd(b.name, b.gitRevision),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
