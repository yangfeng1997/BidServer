package internal

import "testing"

func TestNewGame(t *testing.T) {
	g := NewGame("1.8.1-1", 5, 30, "gold", []Participant{{UID: 1, LobbyNodeID: "1.2.1"}, {UID: 2, LobbyNodeID: "1.2.1"}})
	if g.GameID != "1.8.1-1" || g.ItemID != 5 || g.CountdownSec != 30 {
		t.Fatalf("game fields mismatch: %+v", g)
	}
	if len(g.Participants) != 2 || g.Participants[0].UID != 1 {
		t.Fatalf("participants mismatch: %+v", g.Participants)
	}
}
