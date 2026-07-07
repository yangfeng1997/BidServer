// Package mongo 提供 MongoDB 直连封装：连接管理 + 异步 CRUD。
// 异步方法在 off-loop goroutine 执行阻塞 IO，完成后把回调经 dispatcher
// 投递回调用方主循环（镜像 cluster.Call 的回调投递语义），契合帧驱动零锁服务。
package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	driver "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"project/src/common/taskqueue"
)

// Client MongoDB 客户端封装
type Client struct {
	cli    *driver.Client
	db     *driver.Database
	ctx    context.Context    // 异步 CRUD 的 base ctx，Close 时取消（卡死 op 的兜底）
	cancel context.CancelFunc // 取消 ctx，断开所有在途异步 op
}

// Connect 连接 MongoDB 并 Ping 校验，timeout 控制连接+Ping 总时长
func Connect(uri, dbName string, timeout time.Duration) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cli, err := driver.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	if err := cli.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("mongo ping: %w", err)
	}
	bctx, cancel := context.WithCancel(context.Background())
	return &Client{cli: cli, db: cli.Database(dbName), ctx: bctx, cancel: cancel}, nil
}

// Close 断开连接
func (c *Client) Close() error {
	if c.cancel != nil {
		c.cancel() // 先取消 base ctx，断开所有在途异步 op（卡死兜底）
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.cli.Disconnect(ctx)
}

// runAsync 在 off-loop goroutine 执行 op，完成后把 done(err) 投递回 dispatcher（主循环串行执行）
func runAsync(d taskqueue.Dispatcher, op func() error, done func(error)) {
	go func() {
		err := op()
		d.Enqueue(func() { done(err) })
	}()
}

// FindByID 异步按 _id 读文档解码进 out；done(found, err) 在 dispatcher 执行。
// 文档不存在时 found=false、err=nil。
func (c *Client) FindByID(d taskqueue.Dispatcher, coll string, id any, out any, done func(found bool, err error)) {
	found := false
	runAsync(d, func() error {
		err := c.db.Collection(coll).FindOne(c.ctx, bson.M{"_id": id}).Decode(out)
		if errors.Is(err, driver.ErrNoDocuments) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	}, func(err error) { done(found, err) })
}

// UpsertSetByID 异步对 _id 文档做 $set（upsert）；done(err) 在 dispatcher 执行。
// 绝对状态写，天然幂等（重复 flush 不双加）。
func (c *Client) UpsertSetByID(d taskqueue.Dispatcher, coll string, id any, set bson.M, done func(error)) {
	runAsync(d, func() error {
		_, err := c.db.Collection(coll).UpdateByID(c.ctx, id,
			bson.M{"$set": set}, options.Update().SetUpsert(true))
		return err
	}, done)
}

// InsertOne 异步插入单文档；done(insertedID, err) 在 dispatcher 执行。
func (c *Client) InsertOne(d taskqueue.Dispatcher, coll string, doc any, done func(insertedID any, err error)) {
	var insertedID any
	runAsync(d, func() error {
		res, err := c.db.Collection(coll).InsertOne(c.ctx, doc)
		if err != nil {
			return err
		}
		insertedID = res.InsertedID
		return nil
	}, func(err error) { done(insertedID, err) })
}

// Find 异步按 filter 查多条，sort/limit 可选（limit<=0 不限），结果解码进 out（指向切片的指针）；
// done(err) 在 dispatcher 执行。
func (c *Client) Find(d taskqueue.Dispatcher, coll string, filter bson.M, sort bson.D, limit int64, out any, done func(error)) {
	runAsync(d, func() error {
		opt := options.Find()
		if len(sort) > 0 {
			opt.SetSort(sort)
		}
		if limit > 0 {
			opt.SetLimit(limit)
		}
		cur, err := c.db.Collection(coll).Find(c.ctx, filter, opt)
		if err != nil {
			return err
		}
		return cur.All(c.ctx, out)
	}, done)
}

// UpdateByID 异步对 _id 文档应用任意 update（如 $push/$pull/$set），upsert 可选；done(err) 在 dispatcher 执行。
// 用于离线消息 append($push)/确认($pull) 等增量原子写。
func (c *Client) UpdateByID(d taskqueue.Dispatcher, coll string, id any, update bson.M, upsert bool, done func(error)) {
	runAsync(d, func() error {
		_, err := c.db.Collection(coll).UpdateByID(c.ctx, id, update, options.Update().SetUpsert(upsert))
		return err
	}, done)
}

// FindOneAndUpdate 异步原子「查并更新」：匹配 filter 的单文档应用 update，
// returnUpdated=true 时 out 解码为更新后文档，否则更新前。无匹配时 found=false、err=nil。
// done(found, err) 在 dispatcher 执行。
func (c *Client) FindOneAndUpdate(d taskqueue.Dispatcher, coll string, filter bson.M, update bson.M, returnUpdated bool, out any, done func(found bool, err error)) {
	found := false
	runAsync(d, func() error {
		opt := options.FindOneAndUpdate()
		if returnUpdated {
			opt.SetReturnDocument(options.After)
		}
		err := c.db.Collection(coll).FindOneAndUpdate(c.ctx, filter, update, opt).Decode(out)
		if errors.Is(err, driver.ErrNoDocuments) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	}, func(err error) { done(found, err) })
}
