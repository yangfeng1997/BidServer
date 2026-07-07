package internal

import "testing"

func TestRating_SeedDefault(t *testing.T) {
	r := NewRating()
	if r.MMR() != defaultMMR {
		t.Fatalf("new rating want mmr=%d, got %d", defaultMMR, r.MMR())
	}
}

func TestRating_LoadLegacyZeroSeedsDefault(t *testing.T) {
	r := NewRating()
	r.Load(&RatingState{MMR: 0}) // 旧档缺 rating 字段 → 解码为零值
	if r.MMR() != defaultMMR {
		t.Fatalf("legacy zero mmr should seed default %d, got %d", defaultMMR, r.MMR())
	}
}

func TestRating_LoadExisting(t *testing.T) {
	r := NewRating()
	r.Load(&RatingState{MMR: 1500})
	if r.MMR() != 1500 {
		t.Fatalf("want 1500, got %d", r.MMR())
	}
	if r.Dirty() {
		t.Fatalf("load should clear dirty")
	}
	if s, ok := r.Snapshot().(RatingState); !ok || s.MMR != 1500 {
		t.Fatalf("snapshot mismatch: %#v", r.Snapshot())
	}
}

func TestRating_ComponentContract(t *testing.T) {
	r := NewRating()
	if r.Name() != RatingComponentName || r.Field() != RatingField {
		t.Fatalf("name/field mismatch: %s/%s", r.Name(), r.Field())
	}
}

func TestBuildPlayer_RegistersRating(t *testing.T) {
	p := buildPlayer(1, NewPlayerDoc(1))
	if p.Rating() == nil {
		t.Fatalf("buildPlayer should register rating component")
	}
	if p.Rating().MMR() != defaultMMR {
		t.Fatalf("new player mmr want %d, got %d", defaultMMR, p.Rating().MMR())
	}
}
