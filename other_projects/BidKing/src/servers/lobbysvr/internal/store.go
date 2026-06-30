package internal

import (
	"go.mongodb.org/mongo-driver/bson"

	"project/src/common/mongo"
	"project/src/common/taskqueue"
)

const playersColl = "players"

// PlayerDoc players 集合的整玩家文档：_id=player_id + 各组件内嵌子文档
type PlayerDoc struct {
	ID       int64         `bson:"_id"`
	Bag      BagState      `bson:"bag"`
	Currency CurrencyState `bson:"currency"`
	Friend   FriendState   `bson:"friend"`
	Rating   RatingState   `bson:"rating"`
}

// NewPlayerDoc 创建空白玩家文档（各组件初始化为零值）
func NewPlayerDoc(uid int64) *PlayerDoc {
	return &PlayerDoc{ID: uid, Bag: NewBagState(), Currency: NewCurrencyState(), Friend: NewFriendState(), Rating: NewRatingState()}
}

// DocStore 玩家持久化抽象（便于 Runtime 单测用 fake 替换真实 Mongo）
type DocStore interface {
	Load(d taskqueue.Dispatcher, uid int64, done func(doc *PlayerDoc, found bool, err error))
	FlushFields(d taskqueue.Dispatcher, uid int64, fields map[string]any, done func(error))
}

// mongoStore 基于 src/common/mongo 的 DocStore 实现
type mongoStore struct{ c *mongo.Client }

// NewMongoStore 用已连接的 mongo.Client 构建 mongoStore
func NewMongoStore(c *mongo.Client) DocStore { return &mongoStore{c: c} }

func (s *mongoStore) Load(d taskqueue.Dispatcher, uid int64, done func(*PlayerDoc, bool, error)) {
	doc := &PlayerDoc{}
	s.c.FindByID(d, playersColl, uid, doc, func(found bool, err error) {
		if err != nil || !found {
			done(nil, found, err)
			return
		}
		done(doc, true, nil)
	})
}

func (s *mongoStore) FlushFields(d taskqueue.Dispatcher, uid int64, fields map[string]any, done func(error)) {
	set := make(bson.M, len(fields))
	for field, state := range fields {
		set[field] = state
	}
	s.c.UpsertSetByID(d, playersColl, uid, set, done) // 单文档多字段 $set 原子
}

// 编译期断言 mongoStore 满足 DocStore
var _ DocStore = (*mongoStore)(nil)

// buildPlayer 手写组装玩家实体：按 doc 加载各组件
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

// Bag 返回玩家背包组件（不存在或类型不符返回 nil）
func (p *Player) Bag() *Bag {
	bag, _ := p.Component(BagComponentName).(*Bag)
	return bag
}

// Currency 返回玩家货币组件（不存在或类型不符返回 nil）
func (p *Player) Currency() *Currency {
	c, _ := p.Component(CurrencyComponentName).(*Currency)
	return c
}

// Friend 返回玩家好友组件（不存在或类型不符返回 nil）
func (p *Player) Friend() *Friend {
	f, _ := p.Component(FriendComponentName).(*Friend)
	return f
}

// Rating 返回玩家评分组件（不存在或类型不符返回 nil）
func (p *Player) Rating() *Rating {
	r, _ := p.Component(RatingComponentName).(*Rating)
	return r
}
