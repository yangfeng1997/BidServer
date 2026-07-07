// Package message 定义消息编码格式（Request/Notify/Response/Push），
// 支持路由字典压缩与 zlib 数据压缩。
package message

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Type 表示消息类型。
type Type byte

const (
	Request  Type = 0x00 // 请求-响应模式
	Notify   Type = 0x01 // 通知（无需响应）
	Response Type = 0x02 // 响应
	Push     Type = 0x03 // 服务端推送
)

const (
	errorMask            = 0x20
	gzipMask             = 0x10
	msgRouteCompressMask = 0x01
	msgTypeMask          = 0x07
	msgRouteLengthMask   = 0xFF
	msgHeadLength        = 0x02
)

var types = map[Type]string{
	Request:  "请求",
	Notify:   "通知",
	Response: "响应",
	Push:     "推送",
}

var (
	routesCodesMutex = sync.RWMutex{}
	routes           = make(map[string]uint16)
	codes            = make(map[uint16]string)
)

var (
	ErrWrongMessageType  = errors.New("消息类型错误")
	ErrInvalidMessage    = errors.New("消息无效")
	ErrRouteInfoNotFound = errors.New("路由字典中未找到路由信息")
)

// Message 表示一个已解码或待编码的消息。
type Message struct {
	Type       Type   // 消息类型
	ID         uint   // 事务 ID（Request/Response 下有意义）
	Route      string // 路由字符串（如 "Room.Join"）
	Data       []byte // 有效载荷
	compressed bool   // 路由是否使用字典压缩
	Err        bool   // 是否为错误消息
}

// New 创建新消息，可选错误标记。
func New(err ...bool) *Message {
	m := &Message{}
	if len(err) > 0 {
		m.Err = err[0]
	}
	return m
}

func (m *Message) String() string {
	return fmt.Sprintf("类型: %s, ID: %d, 路由: %s, 压缩: %t, 错误: %t, 数据长度: %d",
		types[m.Type], m.ID, m.Route, m.compressed, m.Err, len(m.Data))
}

func routable(t Type) bool {
	return t == Request || t == Notify || t == Push
}

func invalidType(t Type) bool {
	return t < Request || t > Push
}

// SetDictionary 设置路由压缩字典（必须在服务启动前调用）。
// dict 映射路由字符串 -> uint16 编码。
func SetDictionary(dict map[string]uint16) error {
	if dict == nil {
		return nil
	}
	routesCodesMutex.Lock()
	defer routesCodesMutex.Unlock()

	for route, code := range dict {
		r := strings.TrimSpace(route)
		if r == "" {
			continue
		}

		if existing, ok := routes[r]; ok {
			return fmt.Errorf("路由重复（路由: %s, 编码: %d, 已有编码: %d）", r, code, existing)
		}
		if existing, ok := codes[code]; ok {
			return fmt.Errorf("编码重复（编码: %d, 路由: %s, 已有路由: %s）", code, r, existing)
		}

		routes[r] = code
		codes[code] = r
	}
	return nil
}

// GetDictionary 获取路由压缩字典的副本。
func GetDictionary() map[string]uint16 {
	routesCodesMutex.RLock()
	defer routesCodesMutex.RUnlock()
	dict := make(map[string]uint16, len(routes))
	for k, v := range routes {
		dict[k] = v
	}
	return dict
}
