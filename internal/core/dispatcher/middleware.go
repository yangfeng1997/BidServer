package dispatcher

import (
	"fmt"
	"runtime/debug"

	"project/internal/core/codec"
	"project/internal/core/errcode"
	"project/internal/core/session"
)

// 捕获 handler panic，转为 ERR_INTERNAL 错误
func RecoverMiddleware() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(sess *session.Session, msg *codec.Message) (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = errcode.New(errcode.ERR_INTERNAL,
						fmt.Sprintf("panic recovered: %v\n%s", r, string(debug.Stack())))
				}
			}()
			return next(sess, msg)
		}
	}
}

// 检查 session.Authed.Authed；白名单中的 cmdID 免鉴权
func AuthMiddleware(whitelist map[uint32]bool) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(sess *session.Session, msg *codec.Message) error {
			if msg == nil {
				return errcode.New(errcode.ERR_UNMARSHAL, "nil message")
			}
			if whitelist[msg.CmdID] {
				return next(sess, msg)
			}
			if sess == nil || !sess.Authed {
				return errcode.New(errcode.ERR_UNAUTHED, "session not authenticated")
			}
			return next(sess, msg)
		}
	}
}
