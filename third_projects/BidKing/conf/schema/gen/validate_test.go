package gen

import (
	"strings"
	"testing"
)

func TestValidate_LogConfig(t *testing.T) {
	// 空 LogConfig：dir 和 basename 是 required
	if errs := (&LogConfig{}).Validate(); len(errs) == 0 {
		t.Fatal("expected required errors for empty LogConfig")
	}

	// Level=bad：枚举值不合法
	if errs := (&LogConfig{Dir: "d", Basename: "b", Level: "bad"}).Validate(); len(errs) == 0 {
		t.Fatal("expected enum error for Level=bad")
	}

	// 合法：required 满足 + enum 值合法
	if errs := (&LogConfig{Dir: "d", Basename: "b", Level: "info", Format: "json"}).Validate(); len(errs) != 0 {
		t.Fatalf("expected clean, got %v", errs)
	}
}

func TestValidate_CommonConfig(t *testing.T) {
	// 空 CommonConfig：node/etcd/redis 均非 required message 字段，nil 合法
	if errs := (&CommonConfig{}).Validate(); len(errs) != 0 {
		t.Fatalf("expected clean for empty CommonConfig, got %v", errs)
	}
	// node 存在但内部 required 字段缺失：应报错
	cfg := &CommonConfig{Node: &NodeConfig{}}
	if errs := cfg.Validate(); len(errs) == 0 {
		t.Fatal("expected errors for Node with missing world_id/server_type")
	}
}

func TestValidate_GateConfig_Required(t *testing.T) {
	// listen_tcp 和 listen_ws 是 required
	errs := (&GateConfig{}).Validate()
	if len(errs) == 0 {
		t.Fatal("expected required errors for empty GateConfig")
	}

	// max_conn 是 required
	ok := &GateConfig{ListenTcp: "a", ListenWs: "b"}
	if errs := ok.Validate(); len(errs) == 0 {
		t.Fatal("expected max_conn required error")
	}
}

func TestValidate_EnvNotInjected(t *testing.T) {
	// RedisConfig.password 标记了 env=true，值仍含 ${...} 占位符需报错
	redis := &RedisConfig{Host: "127.0.0.1", Port: 6379, Password: "${REDIS_PWD}"}
	errs := redis.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e, "env var not injected") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'env var not injected' error for password=${REDIS_PWD}, got %v", errs)
	}

	// 环境变量已注入后的值不放行
	redis.Password = "injected_password_value"
	if errs := redis.Validate(); len(errs) != 0 {
		t.Fatalf("expected clean after env injected, got %v", errs)
	}
}

func TestValidate_nil(t *testing.T) {
	var c *GateConfig
	if errs := c.Validate(); len(errs) == 0 || errs[0] != "<nil>" {
		t.Fatalf("expected <nil> error, got %v", errs)
	}
}
