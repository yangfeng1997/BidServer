package internal

import "testing"

func TestCurrency_GainSpend(t *testing.T) {
	c := NewCurrency()
	if bal, changed := c.Gain("op1", "gold", 100); bal != 100 || !changed {
		t.Fatalf("gain: %d %v", bal, changed)
	}
	if !c.CanAfford("gold", 100) || c.CanAfford("gold", 101) {
		t.Fatal("CanAfford wrong")
	}
	if bal, ok := c.Spend("op2", "gold", 30); bal != 70 || !ok {
		t.Fatalf("spend: %d %v", bal, ok)
	}
}

func TestCurrency_SpendInsufficient_NoRememberNoChange(t *testing.T) {
	c := NewCurrency()
	c.Gain("g", "gold", 10)
	bal, ok := c.Spend("op", "gold", 50)
	if ok || bal != 10 {
		t.Fatalf("insufficient spend must fail clean: %d %v", bal, ok)
	}
	// 不足未 remember：充值后同 opID 可成功
	c.Gain("g2", "gold", 100)
	if bal, ok := c.Spend("op", "gold", 50); !ok || bal != 60 {
		t.Fatalf("retry after topup should succeed: %d %v", bal, ok)
	}
}

func TestCurrency_SpendIdempotent(t *testing.T) {
	c := NewCurrency()
	c.Gain("g", "gold", 100)
	c.Spend("opX", "gold", 40) // 60
	if bal, ok := c.Spend("opX", "gold", 40); !ok || bal != 60 {
		t.Fatalf("dup spend must be idempotent success: %d %v", bal, ok)
	}
}

func TestCurrency_LoadSnapshotDirty(t *testing.T) {
	c := NewCurrency()
	c.Load(&CurrencyState{Balances: map[string]int64{"gold": 5}})
	if c.Dirty() {
		t.Fatal("freshly loaded must be clean")
	}
	c.Gain("op", "gold", 1)
	if !c.Dirty() {
		t.Fatal("gain should dirty")
	}
	snap := c.Snapshot().(CurrencyState)
	if snap.Balances["gold"] != 6 {
		t.Fatalf("snapshot: %v", snap.Balances)
	}
	if c.Name() != CurrencyComponentName || c.Field() != CurrencyField {
		t.Fatal("names")
	}
}

func TestCurrency_OpsPersistRoundtrip(t *testing.T) {
	c := NewCurrency()
	c.Gain("op1", "gold", 100)
	c.Spend("op2", "gold", 30)
	snap := c.Snapshot().(CurrencyState)
	if len(snap.Ops) != 2 {
		t.Fatalf("want 2 persisted ops, got %v", snap.Ops)
	}
	// 重建：op1/op2 应被去重（幂等）
	c2 := NewCurrency()
	c2.Load(&snap)
	if _, changed := c2.Gain("op1", "gold", 999); changed {
		t.Fatalf("op1 should be deduped after Load")
	}
	if bal, ok := c2.Spend("op2", "gold", 999); !ok || bal != snap.Balances["gold"] {
		t.Fatalf("op2 should be idempotent-success after Load")
	}
}
