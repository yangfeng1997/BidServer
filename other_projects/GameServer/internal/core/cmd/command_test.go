package cmd

import (
	"testing"
	"time"

	"github.com/spf13/cobra"

	"project/internal/core/app"
)

type testOptions struct {
	app.BaseOptions
	ListenAddr string
}

func newTestOptions() *testOptions {
	opt := &testOptions{}
	opt.Defaults()
	opt.ListenAddr = "127.0.0.1:7001"
	return opt
}

func (o *testOptions) Base() *app.BaseOptions { return &o.BaseOptions }
func (o *testOptions) Defaults() {
	o.BaseOptions.Defaults()
	o.ListenAddr = "127.0.0.1:7001"
}

func TestCommandBindsCommonAndExtraFlags(t *testing.T) {
	var got *testOptions
	cmd := Command[*testOptions](
		app.CommandMeta{Use: "testsvr", Short: "测试服务", Confs: []string{"run/test/conf/"}},
		newTestOptions,
		func(cmd *cobra.Command, opt *testOptions) {
			cmd.Flags().StringVar(&opt.ListenAddr, "listen-addr", opt.ListenAddr, "监听地址")
		},
		func(opt *testOptions) error {
			got = opt
			return nil
		},
	)
	cmd.SetArgs([]string{"-c", "run/common/conf/", "-c", "run/test/conf/", "--listen-addr", "0.0.0.0:9000"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got == nil {
		t.Fatalf("run callback was not called")
	}
	if want := []string{"run/common/conf/", "run/test/conf/"}; len(got.ConfigFiles) != len(want) || got.ConfigFiles[0] != want[0] || got.ConfigFiles[1] != want[1] {
		t.Fatalf("ConfigFiles = %#v, want %#v", got.ConfigFiles, want)
	}
	if got.ListenAddr != "0.0.0.0:9000" {
		t.Fatalf("ListenAddr = %q", got.ListenAddr)
	}
	if got.ReadyTimeout != 10*time.Second {
		t.Fatalf("ReadyTimeout = %s, want 10s", got.ReadyTimeout)
	}
}

func TestCommandVersionSubcommandDoesNotRunMain(t *testing.T) {
	ran := false
	cmd := Command[*testOptions](
		app.CommandMeta{Use: "testsvr", Short: "测试服务", Confs: []string{"run/test/conf/"}},
		newTestOptions,
		nil,
		func(opt *testOptions) error {
			ran = true
			return nil
		},
	)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute version: %v", err)
	}
	if ran {
		t.Fatalf("main run callback should not run for version subcommand")
	}
}
