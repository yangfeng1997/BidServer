package internal

import (
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"project/pkg/mongo"
	"project/pkg/taskqueue"
)

const mailboxColl = "mailbox"

// 邮件类型。
const (
	MailTypeNormal       = "normal"
	MailTypeFriendReq    = "friend_req"
	MailTypeFriendAccept = "friend_accept"
)

// Attachment 邮件附件：Kind=="item" 走背包(ID=itemID)；否则 Kind 视作币种名走货币(Count=数量)。
type Attachment struct {
	Kind  string `bson:"kind"`
	ID    int64  `bson:"id"`
	Count int64  `bson:"count"`
}

// MailID 是 Mongo 邮件 ID 类型。
type MailID = primitive.ObjectID

// MailDoc mailbox 集合文档（独立于 players，insert-only + 原子 claim）。
type MailDoc struct {
	ID          MailID       `bson:"_id,omitempty"`
	To          int64        `bson:"to"`
	From        int64        `bson:"from"`
	Type        string       `bson:"type"`
	Attachments []Attachment `bson:"attachments,omitempty"`
	Body        string       `bson:"body,omitempty"`
	Ts          int64        `bson:"ts"`
	Read        bool         `bson:"read"`
	Claimed     bool         `bson:"claimed"`
}

// MailStore mailbox 持久化抽象。
type MailStore interface {
	Insert(q taskqueue.TaskQueue, m *MailDoc, done func(error))
	List(q taskqueue.TaskQueue, to int64, limit int64, done func([]MailDoc, error))
	Claim(q taskqueue.TaskQueue, id MailID, to int64, done func(claimed bool, m *MailDoc, err error))
	PendingFriendAccepts(q taskqueue.TaskQueue, to int64, done func([]MailDoc, error))
	Get(q taskqueue.TaskQueue, id MailID, to int64, done func(found bool, m *MailDoc, err error))
	MarkClaimed(q taskqueue.TaskQueue, id MailID, done func(error))
}

// mongoMailStore 基于 pkg/mongo 的 MailStore 实现。
type mongoMailStore struct{ c *mongo.Client }

// NewMongoMailStore 用已连接的 mongo.Client 构建。
func NewMongoMailStore(c *mongo.Client) MailStore { return &mongoMailStore{c: c} }

func (s *mongoMailStore) Insert(q taskqueue.TaskQueue, m *MailDoc, done func(error)) {
	s.c.InsertOne(q, mailboxColl, m, func(_ any, err error) { done(err) })
}

func (s *mongoMailStore) List(q taskqueue.TaskQueue, to int64, limit int64, done func([]MailDoc, error)) {
	var out []MailDoc
	s.c.Find(q, mailboxColl, bson.M{"to": to}, bson.D{{Key: "ts", Value: -1}}, limit, &out,
		func(err error) { done(out, err) })
}

func (s *mongoMailStore) Claim(q taskqueue.TaskQueue, id MailID, to int64, done func(bool, *MailDoc, error)) {
	var m MailDoc
	s.c.FindOneAndUpdate(q, mailboxColl,
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

func (s *mongoMailStore) PendingFriendAccepts(q taskqueue.TaskQueue, to int64, done func([]MailDoc, error)) {
	var out []MailDoc
	s.c.Find(q, mailboxColl,
		bson.M{"to": to, "type": MailTypeFriendAccept, "claimed": false},
		bson.D{{Key: "ts", Value: 1}}, 0, &out,
		func(err error) { done(out, err) })
}

func (s *mongoMailStore) Get(q taskqueue.TaskQueue, id MailID, to int64, done func(bool, *MailDoc, error)) {
	var out []MailDoc
	s.c.Find(q, mailboxColl, bson.M{"_id": id, "to": to, "claimed": false}, nil, 1, &out, func(err error) {
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

func (s *mongoMailStore) MarkClaimed(q taskqueue.TaskQueue, id MailID, done func(error)) {
	s.c.UpdateByID(q, mailboxColl, id, bson.M{"$set": bson.M{"claimed": true}}, false, done)
}

var _ MailStore = (*mongoMailStore)(nil)
