package mongo

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/bson"

	"project/src/common/taskqueue"
)

// 编译期断言三新方法签名存在且类型正确（真实行为由 lobbysvr 集成测试覆盖）。
func TestMongoMethodSignatures_Compile(t *testing.T) {
	var c *Client
	var d taskqueue.Dispatcher
	_ = func() {
		c.InsertOne(d, "coll", struct{}{}, func(id any, err error) {})
		c.Find(d, "coll", bson.M{"to": int64(1)}, bson.D{{Key: "ts", Value: -1}}, 50, &[]struct{}{}, func(err error) {})
		c.FindOneAndUpdate(d, "coll", bson.M{"_id": 1}, bson.M{"$set": bson.M{"claimed": true}}, true, &struct{}{}, func(found bool, err error) {})
	}
	_ = c
}

func TestRunAsync_DeliversResultViaDispatcher(t *testing.T) {
	q := taskqueue.New(4)
	sentinel := errors.New("boom")
	var got error
	runAsync(q, func() error { return sentinel }, func(err error) { got = err })

	fn := <-q.C() // 阻塞直到 op 完成并入队（确定性，无需轮询）
	fn()
	if !errors.Is(got, sentinel) {
		t.Fatalf("done not delivered with op error: %v", got)
	}
}

func TestClient_UpdateByID_Compiles(t *testing.T) {
	// 编译验证：签名存在即可（沙箱无 Docker 不实跑）
	var c *Client
	_ = func() {
		c.UpdateByID(nil, "coll", int64(1),
			bson.M{"$push": bson.M{"messages": bson.M{"op_id": "x"}}}, true, func(error) {})
	}
}
