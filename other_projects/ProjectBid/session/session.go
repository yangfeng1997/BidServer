// Package session 定义客户端会话接口与会话池管理。
package session

import (
	"context"
	"errors"
	"net"

	"projectbid/server/networkentity"
)

// 会话错误。
var (
	ErrIllegalUID             = errors.New("非法的用户 ID")
	ErrSessionAlreadyBound    = errors.New("会话已绑定用户 ID")
	ErrOnCloseBackend         = errors.New("OnClose 仅允许在前端服务器调用")
	ErrFrontSessionInvalidOp  = errors.New("前端会话不允许此操作")
	ErrConnectionClosed       = errors.New("客户端连接已关闭")
	ErrReceivedDataUnexpected = errors.New("接收到的数据长度不符合预期")
)

// Session 表示一个客户端连接会话，存储连接存续期间的持久化数据。
type Session interface {
	// ——— 基础属性 ———
	ID() int64
	UID() string
	RemoteAddr() net.Addr

	// ——— 消息通信 ———
	Push(route string, v interface{}) error
	ResponseMID(ctx context.Context, mid uint, v interface{}, isError ...bool) error

	// ——— 用户绑定 ———
	Bind(ctx context.Context, uid string) error
	Kick(ctx context.Context) error

	// ——— 键值数据 ———
	Set(key string, value interface{}) error
	Get(key string) interface{}
	HasKey(key string) bool
	Remove(key string) error
	Clear()
	GetData() map[string]interface{}
	SetData(data map[string]interface{}) error

	// ——— 类型化访问器 ———
	Int(key string) int
	Int8(key string) int8
	Int16(key string) int16
	Int32(key string) int32
	Int64(key string) int64
	Uint(key string) uint
	Uint8(key string) uint8
	Uint16(key string) uint16
	Uint32(key string) uint32
	Uint64(key string) uint64
	Float32(key string) float32
	Float64(key string) float64
	String(key string) string
	Value(key string) interface{}

	// ——— 握手 ———
	SetHandshakeData(data *HandshakeData)
	GetHandshakeData() *HandshakeData
	ValidateHandshake(data *HandshakeData) error

	// ——— 生命周期 ———
	OnClose(c func()) error
	Close()
	GetStatus() int32
	SetStatus(status int32)
}

// HandshakeClientData 客户端在握手包中上报的系统信息。
type HandshakeClientData struct {
	Platform    string `json:"platform"`
	LibVersion  string `json:"libVersion"`
	BuildNumber string `json:"clientBuildNumber"`
	Version     string `json:"clientVersion"`
}

// HandshakeData 客户端握手数据。
type HandshakeData struct {
	Sys  HandshakeClientData    `json:"sys"`
	User map[string]interface{} `json:"user,omitempty"`
}

// SessionPool 集中管理所有会话。
type SessionPool interface {
	NewSession(entity networkentity.NetworkEntity, UID ...string) Session

	GetSessionByUID(uid string) Session
	GetSessionByID(id int64) Session
	GetSessionCount() int64

	OnSessionBind(f func(ctx context.Context, s Session) error)
	OnAfterSessionBind(f func(ctx context.Context, s Session) error)
	OnSessionClose(f func(s Session))
	AddHandshakeValidator(name string, f func(data *HandshakeData) error)

	ForEachSession(f func(s Session))
	CloseAll()
}

// NewSessionPool 创建基于内存的会话池。
func NewSessionPool() SessionPool {
	return newSessionPoolImpl()
}
