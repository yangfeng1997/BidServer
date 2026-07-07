package internal

import (
	"sync"
	"time"

	"project/pkg/timewheel"
)

// Entry 在线目录条目。查询时返回值拷贝，避免外部修改内部状态。
type Entry struct {
	UID           int64
	GatewayNodeID string
	LobbyNodeID   string
	LoginTime     int64
	LastActive    int64
	RoomNodeID    string
	GameID        string
}

// Directory 是全局在线目录的内存实现：map + 单锁 + timewheel 过期。
type Directory struct {
	mu      sync.Mutex
	entries map[int64]*Entry
	timers  map[int64]*timewheel.Timer
	genOf   map[int64]uint64
	nextGen uint64
	tw      *timewheel.TimeWheel
	ttl     time.Duration
}

func NewDirectory(tw *timewheel.TimeWheel, ttl time.Duration) *Directory {
	return &Directory{
		entries: make(map[int64]*Entry),
		timers:  make(map[int64]*timewheel.Timer),
		genOf:   make(map[int64]uint64),
		tw:      tw,
		ttl:     ttl,
	}
}

// Register 注册或刷新在线条目。跨 gateway 重复登录时返回旧条目，调用方可据此踢旧连接。
func (d *Directory) Register(uid int64, gw, lobby string, nowNano int64) (old *Entry, replaced bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var roomNodeID, gameID string
	if e, ok := d.entries[uid]; ok {
		roomNodeID, gameID = e.RoomNodeID, e.GameID
		if e.GatewayNodeID != gw {
			cp := *e
			old, replaced = &cp, true
		}
		if t := d.timers[uid]; t != nil {
			d.tw.Stop(t)
		}
	}
	d.entries[uid] = &Entry{
		UID:           uid,
		GatewayNodeID: gw,
		LobbyNodeID:   lobby,
		LoginTime:     nowNano,
		LastActive:    nowNano,
		RoomNodeID:    roomNodeID,
		GameID:        gameID,
	}
	d.timers[uid] = d.scheduleExpire(uid)
	return old, replaced
}

func (d *Directory) Query(uid int64) (Entry, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.entries[uid]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

func (d *Directory) Unregister(uid int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.entries[uid]; !ok {
		return false
	}
	if t := d.timers[uid]; t != nil {
		d.tw.Stop(t)
	}
	delete(d.entries, uid)
	delete(d.timers, uid)
	delete(d.genOf, uid)
	return true
}

func (d *Directory) Touch(uid int64, nowNano int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.entries[uid]
	if !ok {
		return false
	}
	e.LastActive = nowNano
	if t := d.timers[uid]; t != nil {
		d.tw.Stop(t)
	}
	d.timers[uid] = d.scheduleExpire(uid)
	return true
}

// scheduleExpire 为 uid 发放新代次，迟到的旧回调会被 expire 忽略。
func (d *Directory) scheduleExpire(uid int64) *timewheel.Timer {
	d.nextGen++
	gen := d.nextGen
	d.genOf[uid] = gen
	return d.tw.AfterFunc(d.ttl, func() { d.expire(uid, gen) })
}

func (d *Directory) expire(uid int64, gen uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.genOf[uid] != gen {
		return
	}
	delete(d.entries, uid)
	delete(d.timers, uid)
	delete(d.genOf, uid)
}

func (d *Directory) BindRoom(uid int64, roomNodeID, gameID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.entries[uid]
	if !ok {
		return false
	}
	e.RoomNodeID = roomNodeID
	e.GameID = gameID
	return true
}

func (d *Directory) UnbindRoom(uid int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.entries[uid]
	if !ok {
		return false
	}
	e.RoomNodeID = ""
	e.GameID = ""
	return true
}
