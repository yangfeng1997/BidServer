package cli_test

import (
	"testing"

	"project/src/framework/cli"
)

func TestBuilder_Chain(t *testing.T) {
	b := cli.New("testsvr", "测试服务").
		DefaultConf("run/testsvr/conf/config.yaml").
		GitRevision("abc1234").
		OnStart(func(f *cli.Flags) error {
			return nil
		})

	if b == nil {
		t.Fatal("Builder should not be nil")
	}
}

func TestFlags_Defaults(t *testing.T) {
	var f cli.Flags
	if f.Daemon {
		t.Fatal("Daemon should default to false")
	}
	if f.Addr != "" {
		t.Fatal("Addr should default to empty")
	}
}
