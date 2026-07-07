// Package errors 定义自定义错误类型与错误码体系。
package errors

import (
	"errors"
	"fmt"
)

// 标准错误码常量。
const (
	PIT000 = "PIT-000"
	PIT400 = "PIT-400"
	PIT404 = "PIT-404"
	PIT408 = "PIT-408"
	PIT498 = "PIT-498"
	PIT499 = "PIT-499"
	PIT500 = "PIT-500"
)

// ProjectBidError 带有错误码和可选元数据的项目错误。
type ProjectBidError struct {
	Code     string
	Message  string
	Metadata map[string]string
}

func (e *ProjectBidError) Error() string {
	if e.Metadata != nil && len(e.Metadata) > 0 {
		return fmt.Sprintf("%s - %s (元数据: %v)", e.Code, e.Message, e.Metadata)
	}
	return fmt.Sprintf("%s - %s", e.Code, e.Message)
}

// NewError 创建 ProjectBidError。如果 err 已经包含 ProjectBidError，保留其错误码。
func NewError(err error, code string, metadata ...string) *ProjectBidError {
	var target *ProjectBidError
	if errors.As(err, &target) {
		return target
	}

	meta := make(map[string]string)
	for i := 0; i+1 < len(metadata); i += 2 {
		meta[metadata[i]] = metadata[i+1]
	}

	return &ProjectBidError{
		Code:     code,
		Message:  err.Error(),
		Metadata: meta,
	}
}

// CodeFromError 从错误链中提取错误码，未找到时返回空字符串。
func CodeFromError(err error) string {
	var target *ProjectBidError
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

// StartupError 包装启动失败的组件名称与原始错误。
type StartupError struct {
	Component string
	Err       error
}

func (e *StartupError) Error() string {
	return fmt.Sprintf("%s 启动失败: %v", e.Component, e.Err)
}

func (e *StartupError) Unwrap() error { return e.Err }
