package pipeline

import (
	"context"
	"errors"
	"testing"
)

func TestChannelExecute(t *testing.T) {
	ch := NewChannel()
	order := make([]int, 0)

	ch.PushBack(func(ctx context.Context, in interface{}) (context.Context, interface{}, error) {
		order = append(order, 1)
		return ctx, in, nil
	})
	ch.PushBack(func(ctx context.Context, in interface{}) (context.Context, interface{}, error) {
		order = append(order, 2)
		return ctx, in, nil
	})

	_, _, err := ch.ExecuteBeforePipeline(context.Background(), nil)
	if err != nil {
		t.Fatalf("ExecuteBeforePipeline 失败: %v", err)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Errorf("执行顺序错误: %v", order)
	}
}

func TestChannelExecuteStopsOnError(t *testing.T) {
	ch := NewChannel()
	errTest := errors.New("测试错误")
	calledAfter := false

	ch.PushBack(func(ctx context.Context, in interface{}) (context.Context, interface{}, error) {
		return ctx, in, errTest
	})
	ch.PushBack(func(ctx context.Context, in interface{}) (context.Context, interface{}, error) {
		calledAfter = true
		return ctx, in, nil
	})

	_, _, err := ch.ExecuteBeforePipeline(context.Background(), nil)
	if err != errTest {
		t.Errorf("expected %v, got %v", errTest, err)
	}
	if calledAfter {
		t.Error("错误之后的钩子不应该被调用")
	}
}

func TestAfterChannelExecute(t *testing.T) {
	ch := NewAfterChannel()
	order := make([]int, 0)

	ch.PushBack(func(ctx context.Context, out interface{}, err error) (interface{}, error) {
		order = append(order, 1)
		return out, err
	})
	ch.PushBack(func(ctx context.Context, out interface{}, err error) (interface{}, error) {
		order = append(order, 2)
		return "modified", err
	})

	res, err := ch.ExecuteAfterPipeline(context.Background(), "original", nil)
	if err != nil {
		t.Fatalf("ExecuteAfterPipeline 失败: %v", err)
	}
	if res != "modified" {
		t.Errorf("expected 'modified', got %v", res)
	}
}

func TestEmptyChannels(t *testing.T) {
	ch := NewChannel()
	ctx, data, err := ch.ExecuteBeforePipeline(context.Background(), "test")
	if err != nil || data != "test" {
		t.Error("空 Channel 应透传数据")
	}
	if ctx == nil {
		t.Error("空 Channel 不应返回 nil context")
	}

	ach := NewAfterChannel()
	res, err := ach.ExecuteAfterPipeline(context.Background(), "test", nil)
	if err != nil || res != "test" {
		t.Error("空 AfterChannel 应透传数据")
	}
}
