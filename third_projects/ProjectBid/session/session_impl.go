package session

import (
	"context"
	"encoding/json"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"projectbid/server/logger"
	"projectbid/server/networkentity"
)

type sessionPoolImpl struct {
	sessionBindCallbacks  []func(ctx context.Context, s Session) error
	afterBindCallbacks    []func(ctx context.Context, s Session) error
	handshakeValidators   map[string]func(data *HandshakeData) error
	sessionCloseCallbacks []func(s Session)
	sessionsByUID         sync.Map
	sessionsByID          sync.Map
	sessionIDSvc          *sessionIDService
	sessionCount          int64
}

func newSessionPoolImpl() *sessionPoolImpl {
	return &sessionPoolImpl{
		sessionBindCallbacks:  make([]func(ctx context.Context, s Session) error, 0),
		afterBindCallbacks:    make([]func(ctx context.Context, s Session) error, 0),
		handshakeValidators:   make(map[string]func(data *HandshakeData) error),
		sessionCloseCallbacks: make([]func(s Session), 0),
		sessionIDSvc:          newSessionIDService(),
	}
}

type sessionIDService struct {
	sid int64
}

func newSessionIDService() *sessionIDService {
	return &sessionIDService{}
}

func (c *sessionIDService) sessionID() int64 {
	return atomic.AddInt64(&c.sid, 1)
}

func (pool *sessionPoolImpl) NewSession(entity networkentity.NetworkEntity, UID ...string) Session {
	s := &sessionImpl{
		id:                  pool.sessionIDSvc.sessionID(),
		entity:              entity,
		data:                make(map[string]interface{}),
		handshakeData:       nil,
		handshakeValidators: pool.handshakeValidators,
		lastTime:            time.Now().Unix(),
		OnCloseCallbacks:    []func(){},
		pool:                pool,
	}
	pool.sessionsByID.Store(s.id, s)
	atomic.AddInt64(&pool.sessionCount, 1)
	if len(UID) > 0 {
		s.uid = UID[0]
	}
	return s
}

func (pool *sessionPoolImpl) GetSessionCount() int64 {
	return atomic.LoadInt64(&pool.sessionCount)
}

func (pool *sessionPoolImpl) GetSessionByUID(uid string) Session {
	if val, ok := pool.sessionsByUID.Load(uid); ok {
		return val.(Session)
	}
	return nil
}

func (pool *sessionPoolImpl) GetSessionByID(id int64) Session {
	if val, ok := pool.sessionsByID.Load(id); ok {
		return val.(Session)
	}
	return nil
}

func (pool *sessionPoolImpl) OnSessionBind(f func(ctx context.Context, s Session) error) {
	sf1 := reflect.ValueOf(f)
	for _, fun := range pool.sessionBindCallbacks {
		if reflect.ValueOf(fun).Pointer() == sf1.Pointer() {
			return
		}
	}
	pool.sessionBindCallbacks = append(pool.sessionBindCallbacks, f)
}

func (pool *sessionPoolImpl) OnAfterSessionBind(f func(ctx context.Context, s Session) error) {
	sf1 := reflect.ValueOf(f)
	for _, fun := range pool.afterBindCallbacks {
		if reflect.ValueOf(fun).Pointer() == sf1.Pointer() {
			return
		}
	}
	pool.afterBindCallbacks = append(pool.afterBindCallbacks, f)
}

func (pool *sessionPoolImpl) OnSessionClose(f func(s Session)) {
	sf1 := reflect.ValueOf(f)
	for _, fun := range pool.sessionCloseCallbacks {
		if reflect.ValueOf(fun).Pointer() == sf1.Pointer() {
			return
		}
	}
	pool.sessionCloseCallbacks = append(pool.sessionCloseCallbacks, f)
}

func (pool *sessionPoolImpl) AddHandshakeValidator(name string, f func(data *HandshakeData) error) {
	pool.handshakeValidators[name] = f
}

func (pool *sessionPoolImpl) CloseAll() {
	logger.Infow("关闭所有会话", "数量", pool.GetSessionCount())
	for pool.GetSessionCount() > 0 {
		pool.sessionsByID.Range(func(_, value interface{}) bool {
			s := value.(Session)
			s.Close()
			return true
		})
		logger.Debugw("关闭所有会话中", "剩余", pool.GetSessionCount())
		if pool.GetSessionCount() > 0 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	logger.Info("所有会话已关闭")
}

func (pool *sessionPoolImpl) ForEachSession(f func(s Session)) {
	pool.sessionsByID.Range(func(_, value interface{}) bool {
		s := value.(Session)
		f(s)
		return true
	})
}

// ——— sessionImpl ———

type sessionImpl struct {
	sync.RWMutex
	id                  int64
	uid                 string
	lastTime            int64
	entity              networkentity.NetworkEntity
	data                map[string]interface{}
	handshakeData       *HandshakeData
	handshakeValidators map[string]func(data *HandshakeData) error
	OnCloseCallbacks    []func()
	pool                *sessionPoolImpl
	status              int32
}

func (s *sessionImpl) updateEncodedData() error {
	// 保持与 Pitaya 兼容——实际 JSON 编码在需要跨节点同步时才使用
	return nil
}

// ——— 基础属性 ———

func (s *sessionImpl) ID() int64       { return s.id }
func (s *sessionImpl) UID() string     { return s.uid }
func (s *sessionImpl) RemoteAddr() net.Addr { return s.entity.RemoteAddr() }

func (s *sessionImpl) GetStatus() int32 {
	s.RLock()
	defer s.RUnlock()
	return s.status
}

func (s *sessionImpl) SetStatus(status int32) {
	s.Lock()
	defer s.Unlock()
	s.status = status
}

// ——— 消息通信 ———

func (s *sessionImpl) Push(route string, v interface{}) error {
	return s.entity.Push(route, v)
}

func (s *sessionImpl) ResponseMID(ctx context.Context, mid uint, v interface{}, isError ...bool) error {
	return s.entity.ResponseMID(ctx, mid, v, isError...)
}

// ——— 用户绑定 ———

func (s *sessionImpl) Bind(ctx context.Context, uid string) error {
	if uid == "" {
		return ErrIllegalUID
	}

	s.Lock()
	if s.uid != "" {
		s.Unlock()
		return ErrSessionAlreadyBound
	}
	s.uid = uid
	s.Unlock()

	for _, cb := range s.pool.sessionBindCallbacks {
		if err := cb(ctx, s); err != nil {
			s.Lock()
			s.uid = ""
			s.Unlock()
			return err
		}
	}

	// 关闭同 UID 的旧会话
	if val, ok := s.pool.sessionsByUID.Load(uid); ok {
		val.(Session).Close()
	}
	s.pool.sessionsByUID.Store(uid, s)

	for _, cb := range s.pool.afterBindCallbacks {
		if err := cb(ctx, s); err != nil {
			s.Lock()
			s.uid = ""
			s.Unlock()
			return err
		}
	}

	return nil
}

func (s *sessionImpl) Kick(ctx context.Context) error {
	s.Close()
	return nil
}

// ——— 键值数据 ———

func (s *sessionImpl) Set(key string, value interface{}) error {
	s.Lock()
	defer s.Unlock()
	s.data[key] = value
	return s.updateEncodedData()
}

func (s *sessionImpl) Get(key string) interface{} {
	s.RLock()
	defer s.RUnlock()
	return s.data[key]
}

func (s *sessionImpl) HasKey(key string) bool {
	s.RLock()
	defer s.RUnlock()
	_, ok := s.data[key]
	return ok
}

func (s *sessionImpl) Remove(key string) error {
	s.Lock()
	defer s.Unlock()
	delete(s.data, key)
	return s.updateEncodedData()
}

func (s *sessionImpl) Clear() {
	s.Lock()
	defer s.Unlock()
	s.uid = ""
	s.data = make(map[string]interface{})
	s.updateEncodedData()
}

func (s *sessionImpl) GetData() map[string]interface{} {
	s.RLock()
	defer s.RUnlock()
	return s.data
}

func (s *sessionImpl) SetData(data map[string]interface{}) error {
	s.Lock()
	defer s.Unlock()
	s.data = data
	return s.updateEncodedData()
}

// ——— 类型化访问器 ———

func (s *sessionImpl) Int(key string) int {
	v, ok := s.Get(key).(int)
	if !ok { return 0 }
	return v
}

func (s *sessionImpl) Int8(key string) int8 {
	v, ok := s.Get(key).(int8)
	if !ok { return 0 }
	return v
}

func (s *sessionImpl) Int16(key string) int16 {
	v, ok := s.Get(key).(int16)
	if !ok { return 0 }
	return v
}

func (s *sessionImpl) Int32(key string) int32 {
	v, ok := s.Get(key).(int32)
	if !ok { return 0 }
	return v
}

func (s *sessionImpl) Int64(key string) int64 {
	v, ok := s.Get(key).(int64)
	if !ok { return 0 }
	return v
}

func (s *sessionImpl) Uint(key string) uint {
	v, ok := s.Get(key).(uint)
	if !ok { return 0 }
	return v
}

func (s *sessionImpl) Uint8(key string) uint8 {
	v, ok := s.Get(key).(uint8)
	if !ok { return 0 }
	return v
}

func (s *sessionImpl) Uint16(key string) uint16 {
	v, ok := s.Get(key).(uint16)
	if !ok { return 0 }
	return v
}

func (s *sessionImpl) Uint32(key string) uint32 {
	v, ok := s.Get(key).(uint32)
	if !ok { return 0 }
	return v
}

func (s *sessionImpl) Uint64(key string) uint64 {
	v, ok := s.Get(key).(uint64)
	if !ok { return 0 }
	return v
}

func (s *sessionImpl) Float32(key string) float32 {
	v, ok := s.Get(key).(float32)
	if !ok { return 0 }
	return v
}

func (s *sessionImpl) Float64(key string) float64 {
	v, ok := s.Get(key).(float64)
	if !ok { return 0 }
	return v
}

func (s *sessionImpl) String(key string) string {
	v, ok := s.Get(key).(string)
	if !ok { return "" }
	return v
}

func (s *sessionImpl) Value(key string) interface{} {
	return s.Get(key)
}

// ——— 握手 ———

func (s *sessionImpl) SetHandshakeData(data *HandshakeData) {
	s.Lock()
	defer s.Unlock()
	s.handshakeData = data
}

func (s *sessionImpl) GetHandshakeData() *HandshakeData {
	s.RLock()
	defer s.RUnlock()
	return s.handshakeData
}

func (s *sessionImpl) ValidateHandshake(data *HandshakeData) error {
	for _, v := range s.handshakeValidators {
		if err := v(data); err != nil {
			return err
		}
	}
	return nil
}

// ——— 生命周期 ———

func (s *sessionImpl) OnClose(c func()) error {
	s.OnCloseCallbacks = append(s.OnCloseCallbacks, c)
	return nil
}

func (s *sessionImpl) Close() {
	// 原子地从池中移除
	if _, ok := s.pool.sessionsByID.LoadAndDelete(s.ID()); ok {
		atomic.AddInt64(&s.pool.sessionCount, -1)
	}
	// 清理 UID 绑定（仅当 ID 匹配，避免误删重连后的新会话）
	if val, ok := s.pool.sessionsByUID.Load(s.UID()); ok {
		if val.(Session).ID() == s.ID() {
			s.pool.sessionsByUID.Delete(s.UID())
		}
	}
	// 执行全局关闭回调
	for _, cb := range s.pool.sessionCloseCallbacks {
		cb(s)
	}
	// 执行会话级关闭回调
	for _, cb := range s.OnCloseCallbacks {
		cb()
	}
	// 关闭底层连接
	if err := s.entity.Close(); err != nil {
		logger.Debugw("关闭底层连接失败", "错误", err)
	}
}

// ——— 内部: JSON 序列化数据（用于跨节点同步）———

func (s *sessionImpl) getEncodedData() ([]byte, error) {
	s.RLock()
	defer s.RUnlock()
	return json.Marshal(s.data)
}

func (s *sessionImpl) setEncodedData(encoded []byte) error {
	if len(encoded) == 0 {
		return nil
	}
	var data map[string]interface{}
	if err := json.Unmarshal(encoded, &data); err != nil {
		return err
	}
	return s.SetData(data)
}
