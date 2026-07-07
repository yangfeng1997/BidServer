package internal

import "testing"

func TestEvents_PlayerLoaded(t *testing.T) {
	ev := NewEvents()
	var got int64
	ev.PlayerLoaded.Subscribe(func(e PlayerLoaded) { got = e.UID })
	ev.PlayerLoaded.Publish(PlayerLoaded{UID: 777})
	if got != 777 {
		t.Fatalf("event not delivered: %d", got)
	}
}

func TestEvents_CurrencyChanged(t *testing.T) {
	ev := NewEvents()
	var got CurrencyChanged
	ev.CurrencyChanged.Subscribe(func(e CurrencyChanged) { got = e })
	ev.CurrencyChanged.Publish(CurrencyChanged{UID: 7, Kind: "gold", Delta: -30})
	if got.UID != 7 || got.Kind != "gold" || got.Delta != -30 {
		t.Fatalf("got %+v", got)
	}
}
