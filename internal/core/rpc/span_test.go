package rpc

import (
	"strings"
	"testing"
	"time"
)

func TestNewSpan(t *testing.T) {
	sp := NewSpan("root")
	if sp == nil {
		t.Fatal("NewSpan returned nil")
	}
}

func TestSpanChild(t *testing.T) {
	root := NewSpan("root")
	child := root.Child("child1")
	grandson := child.Child("grandson")

	if child == nil || grandson == nil {
		t.Fatal("Child returned nil")
	}

	time.Sleep(10 * time.Millisecond)
	grandson.Finish()
	child.Finish()
	root.Finish()

	// TreeString 验证
	if ts, ok := root.(*traceSpan); ok {
		tree := ts.TreeString("")
		if !strings.Contains(tree, "root") {
			t.Error("tree missing root")
		}
		if !strings.Contains(tree, "child1") {
			t.Error("tree missing child1")
		}
		if !strings.Contains(tree, "grandson") {
			t.Error("tree missing grandson")
		}
	}
}

func TestSpanElapsed(t *testing.T) {
	sp := NewSpan("timed").(*traceSpan)
	time.Sleep(5 * time.Millisecond)
	sp.Finish()
	elapsed := sp.Elapsed()
	if elapsed < 5*time.Millisecond {
		t.Errorf("elapsed %v too short", elapsed)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("elapsed %v too long", elapsed)
	}
}

func TestTraceCtx(t *testing.T) {
	ctx := TraceCtx("operation")
	sp := ctx.Span()
	if sp == nil {
		t.Fatal("Span() returned nil")
	}
	// 验证是 traceSpan 而非 nopSpan
	if _, ok := sp.(*traceSpan); !ok {
		t.Error("Span should be *traceSpan")
	}
}

func TestRootSpan(t *testing.T) {
	ctx := TraceCtx("root-op")
	root := RootSpan(ctx)
	if root == nil {
		t.Fatal("RootSpan returned nil")
	}
}

func TestNopSpan(t *testing.T) {
	ctx := Background()
	sp := ctx.Span()
	sp.Child("test")
	sp.Finish()
	// nopSpan 不应 panic
}

func TestSpanTreeString(t *testing.T) {
	root := NewSpan("root").(*traceSpan)
	a := root.Child("a").(*traceSpan)
	a.Child("a1").Finish()
	a.Finish()
	root.Child("b").Finish()
	root.Finish()

	tree := root.TreeString("")
	if !strings.Contains(tree, "root") {
		t.Error("missing root")
	}
	if !strings.Contains(tree, "a") {
		t.Error("missing a")
	}
	if !strings.Contains(tree, "b") {
		t.Error("missing b")
	}
	if !strings.Contains(tree, "a1") {
		t.Error("missing a1")
	}
}

func TestSpanName(t *testing.T) {
	sp := NewSpan("my-span").(*traceSpan)
	if sp.Name() != "my-span" {
		t.Errorf("Name=%q, want my-span", sp.Name())
	}
}
