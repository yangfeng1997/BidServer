package internal

import (
	"google.golang.org/protobuf/proto"
	"project/src/framework/cluster"
)

// replyProto marshal 业务响应并经 Replier 异步回包（err 非 nil 时回错误）。nil-replier 安全。
func replyProto(r cluster.Replier, msg proto.Message, err error) {
	if r == nil {
		return
	}
	if err != nil {
		r.Reply(nil, err)
		return
	}
	data, merr := proto.Marshal(msg)
	if merr != nil {
		r.Reply(nil, merr)
		return
	}
	r.Reply(data, nil)
}
