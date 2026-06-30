package internal

import (
	"sync"
	"time"

	"project/src/common/timewheel"
)

// Entry 在线目录条目（值拷贝返回给调用方，避免外部改内部状态）
type Entry struct {
	Uid           int64
	GatewayNodeID string
	LobbyNodeID   string
	LoginTime     int64 // Unix 纳秒
	LastActive    int64 // Unix 纳秒
	RoomNodeID    string // 当前所属 room NodeID 串（未在局为空）
	GameID        string // 当前对局 gameId（未在局为空）
}

// Directory 全局在线目录的纯内存实现：map + 单锁 + timewheel 过期。
// 不依赖 cluster，便于单测；顶号的 Cast 由 OnlineHandler 处理。
type Directory struct {
	mu      sync.Mutex
	entries map[int64]*Entry
	timers  map[int64]*timewheel.Timer
	genOf   map[int64]uint64 // 每 uid 当前过期代次；迟到回调凭代次自证，被顶替则忽略
	nextGen uint64           // 全局单调代次发号器（持 mu 自增，避免注销后代次复用碰撞）
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

// Register 注册/刷新在线条目。若已存在且 gateway 不同（跨 gateway 重复登录），
// 返回旧条目副本与 replaced=true，调用方据此踢旧 gateway。
func (d *Directory) Register(uid int64, gw, lobby string, nowNano int64) (old *Entry, replaced bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var roomNodeID, gameID string // room 绑定与 gate/lobby 位置正交，重注册保留（P4c-2）
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
		Uid: uid, GatewayNodeID: gw, LobbyNodeID: lobby,
		LoginTime: nowNano, LastActive: nowNano,
		RoomNodeID: roomNodeID, GameID: gameID,
	}
	d.timers[uid] = d.scheduleExpire(uid)
	return old, replaced
}

// Query 返回在线条目副本。
func (d *Directory) Query(uid int64) (Entry, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.entries[uid]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// Unregister 删除条目，返回是否确实删除（幂等）。
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

// Touch 刷新活跃并重置过期定时器，返回目标是否在线。
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

// scheduleExpire 为 uid 发放一个新代次并安排过期回调，返回 timer 句柄。
// 须持 d.mu 调用：代次在 AfterFunc 之前按值写入闭包，借 timewheel 调度的锁
// 建立 happens-before，回调读取该代次无数据竞争（不要改成 AfterFunc 后再回填）。
func (d *Directory) scheduleExpire(uid int64) *timewheel.Timer {
	d.nextGen++
	gen := d.nextGen
	d.genOf[uid] = gen
	return d.tw.AfterFunc(d.ttl, func() { d.expire(uid, gen) })
}

// expire 过期清理（timewheel 回调，运行在 tw 推进 goroutine，与 RPC handler 不同
// goroutine）。timewheel 在持锁阶段出队任务、释放锁后才回调，故一个旧代次回调可能
// 在 Register/Touch 顶替条目（或 Unregister）后才迟到触发。这里校验"自上次活跃以来
// 代次未被刷新"（spec §6.3 防 Touch/到期竞态）：若 genOf[uid] 已非本回调代次，说明
// 已被顶替或注销，忽略该迟到回调，绝不误删新代次条目。
func (d *Directory) expire(uid int64, gen uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.genOf[uid] != gen {
		return // 已被后续 Register/Touch 顶替或已注销：迟到回调，忽略
	}
	delete(d.entries, uid)
	delete(d.timers, uid)
	delete(d.genOf, uid)
}

// BindRoom 在在线条目上绝对覆盖写 room 绑定字段；条目不在线返回 false。幂等（重复同值安全）。
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

// UnbindRoom 清除 room 绑定字段；条目不在线返回 false（幂等）。P4a 仅建好，wiring 留 P4b 结算清亲和。
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
