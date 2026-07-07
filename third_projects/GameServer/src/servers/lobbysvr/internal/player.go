package internal

// Component lobby 实体组件接口（仅 lobby 用）。Load 各组件自定义类型化方法，
// 不入接口；接口只覆盖通用 flush 路径（Field/Snapshot/Dirty）。
type Component interface {
	Name() string  // 组件唯一名
	Field() string // 在 players 文档中的 bson 字段名
	Snapshot() any // 可落库的状态快照（值拷贝，bson-able）
	Dirty() bool   // 有未落库变更
	ClearDirty()   // 落库成功后清脏
	MarkDirty()    // 落库失败后重置脏，下次重试
}

// Player 玩家实体：player_id + 组件集合，主循环独占、零锁
type Player struct {
	uid          int64
	components   map[string]Component
	order        []string
	mail         *Mail
	lastTouch    int64        // 上次 Touch onlinesvr 的 Unix 纳秒（Touch 节流用，F2）
	roomAffinity *roomBinding // room 亲和（内存运行态，不持久到 PlayerDoc）
}

func NewPlayer(uid int64) *Player {
	return &Player{uid: uid, components: make(map[string]Component)}
}

func (p *Player) UID() int64 { return p.uid }

// AddComponent 手写显式注册（重复注册 panic，编译/启动期暴露错误）
func (p *Player) AddComponent(c Component) {
	if _, ok := p.components[c.Name()]; ok {
		panic("lobby: duplicate component " + c.Name())
	}
	p.components[c.Name()] = c
	p.order = append(p.order, c.Name())
}

func (p *Player) Component(name string) Component { return p.components[name] }

// Components 按注册顺序返回组件
func (p *Player) Components() []Component {
	out := make([]Component, 0, len(p.order))
	for _, n := range p.order {
		out = append(out, p.components[n])
	}
	return out
}

// attachMail 由 Runtime 在加载后装配邮箱（Mail 依赖 runtime mailStore）
func (p *Player) attachMail(store MailStore) { p.mail = NewMail(p.uid, store) }

// Mail 返回邮箱组件（未 attach 返回 nil）
func (p *Player) Mail() *Mail { return p.mail }

// roomBinding 玩家 room 亲和（内存运行态，不持久到 PlayerDoc；权威重连源是 online）。
// 登录加载置 nil；GameStarted 回告置值；P4b 结算清空。
type roomBinding struct {
	roomNodeID string
	gameID     string
	currency   string // 竞拍币种，供出价 CanAfford 校验用
}

// RoomAffinity 返回当前 room 亲和（未在局返回 nil）
func (p *Player) RoomAffinity() *roomBinding { return p.roomAffinity }

// SetRoomAffinity 置 room 亲和（绝对写，幂等）
func (p *Player) SetRoomAffinity(roomNodeID, gameID, currency string) {
	p.roomAffinity = &roomBinding{roomNodeID: roomNodeID, gameID: gameID, currency: currency}
}

// ClearRoomAffinity 清空 room 亲和
func (p *Player) ClearRoomAffinity() { p.roomAffinity = nil }
