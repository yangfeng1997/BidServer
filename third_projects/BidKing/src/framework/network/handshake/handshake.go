package handshake

import "encoding/json"

// Request 客户端握手请求
type Request struct {
	Sys  RequestSys     `json:"sys"`
	User map[string]any `json:"user,omitempty"`
}

type RequestSys struct {
	Version  string `json:"version"`
	Platform string `json:"platform,omitempty"`
}

// Response 服务端握手响应
type Response struct {
	Code int         `json:"code"`
	Sys  ResponseSys `json:"sys"`
}

type ResponseSys struct {
	Heartbeat  int    `json:"heartbeat"`
	Serializer string `json:"serializer"`
	// Dict 已移除：客户端使用数字 MsgID 路由，不需要字符串压缩字典
}

// Validator 握手校验函数，返回 error 则拒绝握手
type Validator func(req *Request) error

func EncodeRequest(req *Request) ([]byte, error)    { return json.Marshal(req) }
func DecodeRequest(data []byte) (*Request, error)   { r := &Request{}; return r, json.Unmarshal(data, r) }
func EncodeResponse(resp *Response) ([]byte, error) { return json.Marshal(resp) }

// OKResponse 构造成功响应
func OKResponse(heartbeatSec int, serializer string) *Response {
	return &Response{
		Code: 200,
		Sys:  ResponseSys{Heartbeat: heartbeatSec, Serializer: serializer},
	}
}

// ErrResponse 构造失败响应
func ErrResponse() *Response { return &Response{Code: 400} }
