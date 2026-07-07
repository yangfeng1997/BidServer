package internal

import (
	"go.mongodb.org/mongo-driver/bson"

	"project/pkg/mongo"
	"project/pkg/taskqueue"
)

const playersColl = "players"

// PlayerDoc players 集合的整玩家文档：_id=player_id + 各组件内嵌子文档。
type PlayerDoc struct {
	ID       int64         `bson:"_id"`
	Bag      BagState      `bson:"bag"`
	Currency CurrencyState `bson:"currency"`
	Friend   FriendState   `bson:"friend"`
	Rating   RatingState   `bson:"rating"`
}

// NewPlayerDoc 创建空白玩家文档。
func NewPlayerDoc(uid int64) *PlayerDoc {
	return &PlayerDoc{
		ID:       uid,
		Bag:      NewBagState(),
		Currency: NewCurrencyState(),
		Friend:   NewFriendState(),
		Rating:   NewRatingState(),
	}
}

// DocStore 玩家持久化抽象，Runtime 只依赖该接口。
type DocStore interface {
	Load(q taskqueue.TaskQueue, uid int64, done func(doc *PlayerDoc, found bool, err error))
	FlushFields(q taskqueue.TaskQueue, uid int64, fields map[string]any, done func(error))
}

// mongoStore 基于 pkg/mongo 的 DocStore 实现。
type mongoStore struct{ c *mongo.Client }

// NewMongoStore 用已连接的 mongo.Client 构建 mongoStore。
func NewMongoStore(c *mongo.Client) DocStore { return &mongoStore{c: c} }

func (s *mongoStore) Load(q taskqueue.TaskQueue, uid int64, done func(*PlayerDoc, bool, error)) {
	doc := &PlayerDoc{}
	s.c.FindByID(q, playersColl, uid, doc, func(found bool, err error) {
		if err != nil || !found {
			done(nil, found, err)
			return
		}
		done(doc, true, nil)
	})
}

func (s *mongoStore) FlushFields(q taskqueue.TaskQueue, uid int64, fields map[string]any, done func(error)) {
	set := make(bson.M, len(fields))
	for field, state := range fields {
		set[field] = state
	}
	s.c.UpsertSetByID(q, playersColl, uid, set, done)
}

var _ DocStore = (*mongoStore)(nil)
var _ taskqueue.TaskQueue = (*taskqueue.Queue)(nil)

// buildPlayer 手写组装玩家实体：按 doc 加载各组件。
func buildPlayer(uid int64, doc *PlayerDoc) *Player {
	p := NewPlayer(uid)
	bag := NewBag()
	bag.Load(&doc.Bag)
	p.AddComponent(bag)
	cur := NewCurrency()
	cur.Load(&doc.Currency)
	p.AddComponent(cur)
	fr := NewFriend()
	fr.Load(&doc.Friend)
	p.AddComponent(fr)
	rating := NewRating()
	rating.Load(&doc.Rating)
	p.AddComponent(rating)
	return p
}

// BuildPlayerForTest 暴露给包外测试，生产代码应通过 Runtime 加载玩家。
func BuildPlayerForTest(uid int64, doc *PlayerDoc) *Player { return buildPlayer(uid, doc) }

// Bag 返回玩家背包组件（不存在或类型不符返回 nil）。
func (p *Player) Bag() *Bag {
	bag, _ := p.Component(BagComponentName).(*Bag)
	return bag
}

// Currency 返回玩家货币组件（不存在或类型不符返回 nil）。
func (p *Player) Currency() *Currency {
	c, _ := p.Component(CurrencyComponentName).(*Currency)
	return c
}

// Friend 返回玩家好友组件（不存在或类型不符返回 nil）。
func (p *Player) Friend() *Friend {
	f, _ := p.Component(FriendComponentName).(*Friend)
	return f
}

// Rating 返回玩家评分组件（不存在或类型不符返回 nil）。
func (p *Player) Rating() *Rating {
	r, _ := p.Component(RatingComponentName).(*Rating)
	return r
}
