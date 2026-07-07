// src/servers/lobbysvr/internal/component_mail.go
package internal

import (
	"go.mongodb.org/mongo-driver/bson/primitive"

	"project/src/common/taskqueue"
)

// Mail 邮箱组件：对 mailbox 集合的异步 I/O 句柄。
// 非 flush 组件（不实现 Component 接口、不入 Components()）；持自己的 uid + MailStore。
type Mail struct {
	uid   int64
	store MailStore
}

func NewMail(uid int64, store MailStore) *Mail { return &Mail{uid: uid, store: store} }

// List 拉本玩家收件（limit 上限，按 ts 倒序）
func (m *Mail) List(d taskqueue.Dispatcher, limit int64, done func([]MailDoc, error)) {
	m.store.List(d, m.uid, limit, done)
}

// Claim 领取本玩家某邮件（原子幂等）
func (m *Mail) Claim(d taskqueue.Dispatcher, id primitive.ObjectID, done func(bool, *MailDoc, error)) {
	m.store.Claim(d, id, m.uid, done)
}

// Get 读未领取邮件副本（① 重排用）
func (m *Mail) Get(d taskqueue.Dispatcher, id primitive.ObjectID, done func(bool, *MailDoc, error)) {
	m.store.Get(d, id, m.uid, done)
}

// MarkClaimed grant 落库后标记已领取
func (m *Mail) MarkClaimed(d taskqueue.Dispatcher, id primitive.ObjectID, done func(error)) {
	m.store.MarkClaimed(d, id, done)
}
