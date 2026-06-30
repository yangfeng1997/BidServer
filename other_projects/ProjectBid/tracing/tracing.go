// Package tracing 提供分布式追踪能力，基于 OpenTracing 与 Jaeger。
package tracing

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/uber/jaeger-client-go"
	jaegerConfig "github.com/uber/jaeger-client-go/config"
)

// Config Jaeger 追踪器配置。
type Config struct {
	// ServiceName 服务名称，显示在 Jaeger UI 中。
	ServiceName string
	// AgentHost Jaeger Agent 主机地址。
	AgentHost string
	// AgentPort Jaeger Agent 端口。
	AgentPort string
	// SamplerType 采样类型: "const", "probabilistic", "ratelimiting"。
	SamplerType string
	// SamplerParam 采样参数: const(0或1), probabilistic(0~1), ratelimiting(qps)。
	SamplerParam float64
	// LogSpans 是否记录所有 span。
	LogSpans bool
	// Tags 全局标签。
	Tags map[string]string
}

// DefaultConfig 返回默认配置。
func DefaultConfig(serviceName string) Config {
	return Config{
		ServiceName:  serviceName,
		AgentHost:    "127.0.0.1",
		AgentPort:    "6831",
		SamplerType:  "const",
		SamplerParam: 1,
		LogSpans:     false,
		Tags:         map[string]string{},
	}
}

// Init 初始化全局追踪器。
// 返回的 closer 应在进程退出前调用 Close()。
func Init(cfg Config) (io.Closer, error) {
	if cfg.ServiceName == "" {
		cfg.ServiceName = "projectbid-server"
	}
	if cfg.AgentHost == "" {
		cfg.AgentHost = "127.0.0.1"
	}
	if cfg.AgentPort == "" {
		cfg.AgentPort = "6831"
	}
	if cfg.SamplerType == "" {
		cfg.SamplerType = "const"
	}

	hostname, _ := os.Hostname()

	jCfg := jaegerConfig.Configuration{
		ServiceName: cfg.ServiceName,
		Sampler: &jaegerConfig.SamplerConfig{
			Type:  cfg.SamplerType,
			Param: cfg.SamplerParam,
		},
		Reporter: &jaegerConfig.ReporterConfig{
			LogSpans:            cfg.LogSpans,
			LocalAgentHostPort:  fmt.Sprintf("%s:%s", cfg.AgentHost, cfg.AgentPort),
			BufferFlushInterval: 1 * time.Second,
		},
		Tags: append([]opentracing.Tag{
			opentracing.Tag{Key: "hostname", Value: hostname},
		}, mapToTags(cfg.Tags)...),
	}

	tracer, closer, err := jCfg.NewTracer(
		jaegerConfig.Logger(jaeger.StdLogger),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 Jaeger 追踪器失败: %w", err)
	}

	opentracing.SetGlobalTracer(tracer)
	return closer, nil
}

// StartSpanFromContext 从 context 中提取或创建新的 OpenTracing Span。
func StartSpanFromContext(ctx context.Context, operationName string) (opentracing.Span, context.Context) {
	parent := opentracing.SpanFromContext(ctx)
	if parent != nil {
		span := opentracing.StartSpan(operationName, opentracing.ChildOf(parent.Context()))
		return span, opentracing.ContextWithSpan(ctx, span)
	}
	span := opentracing.GlobalTracer().StartSpan(operationName)
	return span, opentracing.ContextWithSpan(ctx, span)
}

// StartSpanWithTags 创建带标签的 Span。
func StartSpanWithTags(ctx context.Context, operationName string, tags map[string]string) (opentracing.Span, context.Context) {
	span, ctx := StartSpanFromContext(ctx, operationName)
	for k, v := range tags {
		span.SetTag(k, v)
	}
	return span, ctx
}

// InjectToMetadata 将 Span 上下文注入到元数据映射中（用于跨服务传播）。
func InjectToMetadata(span opentracing.Span, metadata map[string]string) error {
	if metadata == nil {
		return nil
	}
	carrier := opentracing.TextMapCarrier(metadata)
	return opentracing.GlobalTracer().Inject(
		span.Context(),
		opentracing.TextMap,
		carrier,
	)
}

// ExtractFromMetadata 从元数据映射中提取 Span 上下文。
func ExtractFromMetadata(ctx context.Context, operationName string, metadata map[string]string) (opentracing.Span, context.Context) {
	carrier := opentracing.TextMapCarrier(metadata)
	spanCtx, err := opentracing.GlobalTracer().Extract(
		opentracing.TextMap,
		carrier,
	)
	if err != nil && err != opentracing.ErrSpanContextNotFound {
		span := opentracing.GlobalTracer().StartSpan(operationName)
		return span, opentracing.ContextWithSpan(ctx, span)
	}

	span := opentracing.GlobalTracer().StartSpan(operationName, opentracing.ChildOf(spanCtx))
	return span, opentracing.ContextWithSpan(ctx, span)
}

// FinishSpan 安全结束 span，处理 nil 情况。
func FinishSpan(span opentracing.Span) {
	if span != nil {
		span.Finish()
	}
}

func mapToTags(m map[string]string) []opentracing.Tag {
	tags := make([]opentracing.Tag, 0, len(m))
	for k, v := range m {
		tags = append(tags, opentracing.Tag{Key: k, Value: v})
	}
	return tags
}
