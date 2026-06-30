// Package errors 定义框架层错误码与错误类型。
//
// 错误分两层：
//   - 框架错误：无法返回正常 Response（无 handler、panic、超时），用本包的负数 Code，
//     编码进帧层 Response.Code 回给客户端。
//   - 业务错误：业务逻辑的正常失败结果（金币不足、等级不够），是 resp body 里的字段，
//     由游戏 proto 自己定义正数错误码，不走本包，不进帧层。
package errors

// Code 框架错误码。约定：
//   - 0   成功
//   - 负数 框架保留错误码
//   - 正数 业务错误码（不在本包定义，走 resp body）
type Code = int32

const (
	OK         Code = 0
	BadRequest Code = -400 // 请求格式错误（通常直接断连，少回）
	NotFound   Code = -404 // 无对应 handler（路由表缺 MsgID）
	Timeout    Code = -408 // 集群 RPC 超时
	Internal   Code = -500 // handler panic 或框架内部错误
)

// Error 框架错误，handler 可返回它携带特定 Code。
// 普通 error（如 panic 转换的）默认映射为 Internal(-500)。
type Error struct {
	Code Code
	Msg  string // 仅日志/调试用，不强制传客户端
}

func (e *Error) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return codeName(e.Code)
}

// New 构造框架错误
func New(c Code, msg string) *Error { return &Error{Code: c, Msg: msg} }

// CodeOf 从任意 error 提取 Code：
//   - nil           → OK(0)
//   - *Error        → 其 Code
//   - 其他 error     → Internal(-500)
func CodeOf(err error) Code {
	if err == nil {
		return OK
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return Internal
}

func codeName(c Code) string {
	switch c {
	case OK:
		return "ok"
	case BadRequest:
		return "bad request"
	case NotFound:
		return "not found"
	case Timeout:
		return "timeout"
	case Internal:
		return "internal error"
	default:
		return "unknown error"
	}
}
