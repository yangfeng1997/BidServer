package errcode

import "fmt"

// 框架统一错误码
// 业务错误码和框架错误码共用这一套数值体系
type ErrCode int32

const (
	OK ErrCode = 0

	ERR_INTERNAL  ErrCode = 1
	ERR_TIMEOUT   ErrCode = 2
	ERR_NO_ROUTE  ErrCode = 3
	ERR_UNAUTHED  ErrCode = 4
	ERR_UNMARSHAL ErrCode = 5
)

// 带错误码的错误
// 业务层和框架层都使用这一接口传递错误码
type Error interface {
	error
	Code() ErrCode
}

type codedError struct {
	code ErrCode
	msg  string
}

func (e *codedError) Error() string { return e.msg }
func (e *codedError) Code() ErrCode { return e.code }

// 创建带错误码的错误
func New(code ErrCode, msg string) Error {
	return &codedError{code: code, msg: msg}
}

// 根据错误码创建错误
func From(code ErrCode) Error {
	return &codedError{code: code, msg: code.String()}
}

// 从 error 提取错误码
// 未知错误返回 ERR_INTERNAL
func CodeOf(err error) ErrCode {
	if err == nil {
		return OK
	}
	if e, ok := err.(Error); ok {
		return e.Code()
	}
	return ERR_INTERNAL
}

// 返回错误码名称
func (c ErrCode) String() string {
	switch c {
	case OK:
		return "OK"
	case ERR_INTERNAL:
		return "ERR_INTERNAL"
	case ERR_TIMEOUT:
		return "ERR_TIMEOUT"
	case ERR_NO_ROUTE:
		return "ERR_NO_ROUTE"
	case ERR_UNAUTHED:
		return "ERR_UNAUTHED"
	case ERR_UNMARSHAL:
		return "ERR_UNMARSHAL"
	default:
		return fmt.Sprintf("ErrCode(%d)", c)
	}
}
