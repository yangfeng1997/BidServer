package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"project/internal/core/app"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func Command[T app.Options](
	meta app.CommandMeta,
	newOptions func() T,
	addFlags func(cmd *cobra.Command, opt T),
	run func(opt T) error,
) *cobra.Command {
	opt := newOptions()
	opt.Base().ConfigFiles = append([]string(nil), meta.Confs...)

	root := &cobra.Command{
		Use:          meta.Use,
		Short:        meta.Short,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			base := opt.Base()
			if base.ValidateOnly {
				return app.ValidateConfig(base.ConfigFiles)
			}
			return run(opt)
		},
	}

	BindCommonFlags(root, opt.Base(), meta.Confs)
	if addFlags != nil {
		addFlags(root, opt)
	}
	AddVersionCmd(root)
	return root
}

func BindCommonFlags(root *cobra.Command, opt *app.BaseOptions, defaultConfs []string) {
	f := root.Flags()
	f.StringArrayVarP(&opt.ConfigFiles, "config", "c", append([]string(nil), defaultConfs...), "配置文件路径，可多次指定")
	f.BoolVar(&opt.ValidateOnly, "validate-config", false, "校验配置后退出（CI 用）")
}

func AddVersionCmd(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "打印版本信息并退出",
		Run: func(*cobra.Command, []string) {
			fmt.Printf("%s version %s (built %s)\n", root.Use, Version, BuildTime)
		},
	})
}
