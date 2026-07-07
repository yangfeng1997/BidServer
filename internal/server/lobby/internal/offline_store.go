package internal

import (
	"go.mongodb.org/mongo-driver/bson"

	"project/pkg/mongo"
	"project/pkg/taskqueue"
)

const offlineColl = "offline_messages"

// 离线消息类型（本期只 settle）。
const OfflineMsgSettle = "settle"

// OfflineMsg 离线消息信封（带 type，便于后续复用）。settle: 离线赢家结算扣币+发物。
type OfflineMsg struct {
	Type     string `bson:"type"`
	OpID     string `bson:"op_id"`
	Price    int64  `bson:"price,omitempty"`
	Currency string `bson:"currency,omitempty"`
	ItemID   int32  `bson:"item_id,omitempty"`
}

// OfflineDoc offline_messages 文档：每玩家一 doc，多写者 append-only。
type OfflineDoc struct {
	ID       int64        `bson:"_id"`
	Messages []OfflineMsg `bson:"messages"`
}

// OfflineStore 离线消息持久化抽象。
type OfflineStore interface {
	Push(q taskqueue.TaskQueue, uid int64, msg OfflineMsg, done func(error))
	Load(q taskqueue.TaskQueue, uid int64, done func([]OfflineMsg, error))
	Ack(q taskqueue.TaskQueue, uid int64, opIDs []string, done func(error))
}

// mongoOfflineStore 基于 pkg/mongo 的实现。
type mongoOfflineStore struct{ c *mongo.Client }

// NewMongoOfflineStore 用已连接的 mongo.Client 构建。
func NewMongoOfflineStore(c *mongo.Client) OfflineStore { return &mongoOfflineStore{c: c} }

func (s *mongoOfflineStore) Push(q taskqueue.TaskQueue, uid int64, msg OfflineMsg, done func(error)) {
	s.c.UpdateByID(q, offlineColl, uid, bson.M{"$push": bson.M{"messages": msg}}, true, done)
}

func (s *mongoOfflineStore) Load(q taskqueue.TaskQueue, uid int64, done func([]OfflineMsg, error)) {
	doc := &OfflineDoc{}
	s.c.FindByID(q, offlineColl, uid, doc, func(found bool, err error) {
		if err != nil || !found {
			done(nil, err)
			return
		}
		done(doc.Messages, nil)
	})
}

func (s *mongoOfflineStore) Ack(q taskqueue.TaskQueue, uid int64, opIDs []string, done func(error)) {
	if len(opIDs) == 0 {
		q.Enqueue(func() { done(nil) })
		return
	}
	s.c.UpdateByID(q, offlineColl, uid,
		bson.M{"$pull": bson.M{"messages": bson.M{"op_id": bson.M{"$in": opIDs}}}}, false, done)
}

var _ OfflineStore = (*mongoOfflineStore)(nil)
