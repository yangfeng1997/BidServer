// src/servers/lobbysvr/internal/mailbox_store.go
package internal

import (
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"project/src/common/mongo"
	"project/src/common/taskqueue"
)

const mailboxColl = "mailbox"

// 邮件类型
const (
	MailTypeNormal       = "normal"
	MailTypeFriendReq    = "friend_req"
	MailTypeFriendAccept = "friend_accept"
)

// Attachment 邮件附件：Kind=="item" 走背包(ID=itemID)；否则 Kind 视作币种名走货币(Count=数量)
type Attachment struct {
	Kind  string `bson:"kind"`
	ID    int64  `bson:"id"`
	Count int64  `bson:"count"`
}

// MailDoc mailbox 集合文档（独立于 players，insert-only + 原子 claim）
type MailDoc struct {
	ID          primitive.ObjectID `bson:"_id,omitempty"`
	To          int64              `bson:"to"`
	From        int64              `bson:"from"`
	Type        string             `bson:"type"`
	Attachments []Attachment       `bson:"attachments,omitempty"`
	Body        string             `bson:"body,omitempty"`
	Ts          int64              `bson:"ts"`
	Read        bool               `bson:"read"`
	Claimed     bool               `bson:"claimed"`
}

// MailStore mailbox 持久化抽象（便于 fake 替换真实 Mongo）
type MailStore interface {
	Insert(d taskqueue.Dispatcher, m *MailDoc, done func(error))
	List(d taskqueue.Dispatcher, to int64, limit int64, done func([]MailDoc, error))
	Claim(d taskqueue.Dispatcher, id primitive.ObjectID, to int64, done func(claimed bool, m *MailDoc, err error))
	PendingFriendAccepts(d taskqueue.Dispatcher, to int64, done func([]MailDoc, error))
	Get(d taskqueue.Dispatcher, id primitive.ObjectID, to int64, done func(found bool, m *MailDoc, err error))
	MarkClaimed(d taskqueue.Dispatcher, id primitive.ObjectID, done func(error))
}

// mongoMailStore 基于 src/common/mongo 的 MailStore 实现
type mongoMailStore struct{ c *mongo.Client }

// NewMongoMailStore 用已连接的 mongo.Client 构建
func NewMongoMailStore(c *mongo.Client) MailStore { return &mongoMailStore{c: c} }

func (s *mongoMailStore) Insert(d taskqueue.Dispatcher, m *MailDoc, done func(error)) {
	s.c.InsertOne(d, mailboxColl, m, func(_ any, err error) { done(err) })
}

func (s *mongoMailStore) List(d taskqueue.Dispatcher, to int64, limit int64, done func([]MailDoc, error)) {
	var out []MailDoc
	s.c.Find(d, mailboxColl, bson.M{"to": to}, bson.D{{Key: "ts", Value: -1}}, limit, &out,
		func(err error) { done(out, err) })
}

// Claim 原子领取：匹配 {_id,to,claimed:false} 置 claimed:true 并返回该文档；无匹配 claimed=false。
func (s *mongoMailStore) Claim(d taskqueue.Dispatcher, id primitive.ObjectID, to int64, done func(bool, *MailDoc, error)) {
	var m MailDoc
	s.c.FindOneAndUpdate(d, mailboxColl,
		bson.M{"_id": id, "to": to, "claimed": false},
		bson.M{"$set": bson.M{"claimed": true}}, true, &m,
		func(found bool, err error) {
			if err != nil || !found {
				done(false, nil, err)
				return
			}
			done(true, &m, nil)
		})
}

func (s *mongoMailStore) PendingFriendAccepts(d taskqueue.Dispatcher, to int64, done func([]MailDoc, error)) {
	var out []MailDoc
	s.c.Find(d, mailboxColl,
		bson.M{"to": to, "type": MailTypeFriendAccept, "claimed": false},
		bson.D{{Key: "ts", Value: 1}}, 0, &out,
		func(err error) { done(out, err) })
}

// Get 读未领取邮件副本（不改状态）：匹配 {_id,to,claimed:false}。
func (s *mongoMailStore) Get(d taskqueue.Dispatcher, id primitive.ObjectID, to int64, done func(bool, *MailDoc, error)) {
	var out []MailDoc
	s.c.Find(d, mailboxColl, bson.M{"_id": id, "to": to, "claimed": false}, nil, 1, &out, func(err error) {
		if err != nil {
			done(false, nil, err)
			return
		}
		if len(out) == 0 {
			done(false, nil, nil)
			return
		}
		done(true, &out[0], nil)
	})
}

// MarkClaimed 标记已领取（grant 落库后调用；单写者下无并发双标）。
func (s *mongoMailStore) MarkClaimed(d taskqueue.Dispatcher, id primitive.ObjectID, done func(error)) {
	s.c.UpdateByID(d, mailboxColl, id, bson.M{"$set": bson.M{"claimed": true}}, false, done)
}

var _ MailStore = (*mongoMailStore)(nil)
