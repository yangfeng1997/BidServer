package internal

import "time"

// Participant 局内参与者（与 roompb.Participant 解耦的内部态）
type Participant struct {
	UID         int64
	LobbyNodeID string
}

// Game 拍卖局对象。P4b：加最高价/赢家/币种/封盘 + 倒计时 deadline。多 gameId 并存隔离。
type Game struct {
	GameID        string
	Participants  []Participant
	ItemID        int32
	CountdownSec  int32
	Currency      string
	HighestBid    int64
	HighestBidder int64
	closed        bool
	deadline      time.Time // 倒计时到点（广播剩余秒用）
}

// NewGame 建局
func NewGame(gameID string, itemID, countdownSec int32, currency string, parts []Participant) *Game {
	return &Game{GameID: gameID, ItemID: itemID, CountdownSec: countdownSec, Currency: currency, Participants: parts}
}

// isParticipant 判断 uid 是否本局参与者
func (g *Game) isParticipant(uid int64) bool {
	for _, p := range g.Participants {
		if p.UID == uid {
			return true
		}
	}
	return false
}

// remaining 倒计时剩余秒（非负）
func (g *Game) remaining() int32 {
	d := int32(time.Until(g.deadline).Seconds())
	if d < 0 {
		return 0
	}
	return d
}
