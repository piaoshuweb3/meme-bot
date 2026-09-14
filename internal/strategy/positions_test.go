package strategy

import (
	"testing"

	"meme-bot/internal/model"
)

func TestMemoryPositionsLifecycle(t *testing.T) {
	m := NewMemoryPositions()
	p := &model.Position{Chain: "base", Token: "T", Status: model.PositionOpen}
	if err := m.Open(p); err != nil {
		t.Fatalf("Open 失败：%v", err)
	}
	if p.ID == "" {
		t.Fatal("未提供 ID 时应自动生成")
	}

	got, err := m.Get(p.ID)
	if err != nil || got == nil {
		t.Fatalf("Get 失败：%v %v", got, err)
	}
	if got.Token != "T" || got.Chain != "base" || got.Status != model.PositionOpen {
		t.Fatalf("字段不匹配：%+v", got)
	}

	// 副本语义：改动返回值不得影响存储内部
	got.Token = "MUTATED"
	again, _ := m.Get(p.ID)
	if again.Token != "T" {
		t.Fatal("Get 应返回副本（避免调用方误改内部状态）")
	}

	open, err := m.ListOpen("base")
	if err != nil || len(open) != 1 {
		t.Fatalf("应列出 1 个开仓：%d %v", len(open), err)
	}
	if l, _ := m.ListOpen("solana"); len(l) != 0 {
		t.Fatalf("链过滤失效：%d", len(l))
	}
	if l, _ := m.ListOpen("BASE"); len(l) != 1 {
		t.Fatalf("链过滤应大小写不敏感：%d", len(l))
	}

	if err := m.Close(p.ID, 1.5, 42.5); err != nil {
		t.Fatalf("Close 失败：%v", err)
	}
	closed, _ := m.Get(p.ID)
	if closed.Status != model.PositionClosed {
		t.Fatalf("状态应为已平仓：%v", closed.Status)
	}
	if closed.ExitPriceUSD != 1.5 || closed.RealizedPnLUSD != 42.5 {
		t.Fatalf("平仓字段错误：%+v", closed)
	}
	if closed.ClosedAt.IsZero() || !closed.ClosedAt.Equal(closed.UpdatedAt) {
		t.Fatalf("平仓时间与更新时间应一致：%+v", closed)
	}
	if l, _ := m.ListOpen(""); len(l) != 0 {
		t.Fatalf("平仓后不应出现在开仓列表：%d", len(l))
	}
}

func TestMemoryPositionsErrorsAndUpdate(t *testing.T) {
	m := NewMemoryPositions()
	if err := m.Open(nil); err == nil {
		t.Fatal("Open(nil) 应报错")
	}
	if err := m.Update(nil); err == nil {
		t.Fatal("Update(nil) 应报错")
	}
	if got, err := m.Get("missing"); err != nil || got != nil {
		t.Fatalf("未知 ID 应返回 (nil,nil)：%v %v", got, err)
	}
	if err := m.Close("missing", 1, 1); err != nil {
		t.Fatalf("Close 未知 ID 应幂等不报错：%v", err)
	}

	p := &model.Position{ID: "p1", Chain: "base", Token: "A", Status: model.PositionOpen}
	if err := m.Open(p); err != nil {
		t.Fatal(err)
	}
	updated := &model.Position{ID: "p1", Chain: "base", Token: "B", Status: model.PositionOpen, RealizedPnLUSD: 7}
	if err := m.Update(updated); err != nil {
		t.Fatalf("Update 失败：%v", err)
	}
	got, _ := m.Get("p1")
	if got.Token != "B" || got.RealizedPnLUSD != 7 {
		t.Fatalf("Update 未覆盖字段：%+v", got)
	}
}
