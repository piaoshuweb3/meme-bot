package risk

import (
	"testing"
	"time"

	"meme-bot/internal/config"
	"meme-bot/internal/model"
)

func newTestEngine() *Engine {
	cfg := config.RiskConfig{
		MaxPositionPct:           0.10,
		MaxOpenPositions:         3,
		StopLossPct:              0.25,
		TrailingStopPct:          0.20,
		LiquidityDropTriggerPct:  0.35,
		DailyLossLimitPct:        0.15,
		CooldownAfterLossMinutes: 60,
		MaxSlippageBps:           150,
		SplitEntries:             3,
	}
	e := New(cfg, NewMemoryStore(), nil, nil, nil)
	e.SetEquity(10000)
	return e
}

func TestCheckEntryRejectsWhenPaused(t *testing.T) {
	e := newTestEngine()
	if err := e.Pause("测试熔断", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("pause: %v", err)
	}

	decision, err := e.CheckEntry(model.EntryRequest{Chain: "base", Token: "0xabc", AmountUSD: 100, LiquidityUSD: 50000})
	if err != nil {
		t.Fatalf("CheckEntry: %v", err)
	}
	if decision.Verdict != model.VerdictReject {
		t.Fatalf("熔断中应拒绝下单，实际 %s", decision.Verdict)
	}

	if err := e.Resume(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if paused, _ := e.Paused(); paused {
		t.Fatal("恢复后不应仍处于熔断状态")
	}
}

func TestCheckEntryReducesOversizedPosition(t *testing.T) {
	e := newTestEngine()
	decision, err := e.CheckEntry(model.EntryRequest{
		Chain: "base", Token: "0xabc", AmountUSD: 5000, LiquidityUSD: 100000,
	})
	if err != nil {
		t.Fatalf("CheckEntry: %v", err)
	}
	if decision.Verdict != model.VerdictReduce {
		t.Fatalf("超限仓位应被缩减，实际 %s", decision.Verdict)
	}
	if decision.AllowedUSD > 1000.0001 {
		t.Fatalf("允许金额应不超过权益的 10%%（1000），实际 %f", decision.AllowedUSD)
	}
	if decision.SplitEntries != 3 {
		t.Fatalf("分批笔数应为 3，实际 %d", decision.SplitEntries)
	}
}

func TestCheckEntryRejectsLowLiquidity(t *testing.T) {
	e := newTestEngine()
	decision, _ := e.CheckEntry(model.EntryRequest{Chain: "base", Token: "0xabc", AmountUSD: 100, LiquidityUSD: 5000})
	if decision.Verdict != model.VerdictReject {
		t.Fatalf("低流动性应被拒绝，实际 %s", decision.Verdict)
	}
}

func TestCheckExitHardStopLoss(t *testing.T) {
	e := newTestEngine()
	pos := &model.Position{
		EntryPriceUSD: 1.0, PeakPriceUSD: 1.0,
		StopLossPct: 0.25, LiquidityAtEntryUSD: 50000,
	}

	// 现价 0.7 ≤ 止损价 0.75 → 触发
	decision, err := e.CheckExit(pos, 0.70, 50000)
	if err != nil {
		t.Fatalf("CheckExit: %v", err)
	}
	if decision.Verdict != model.VerdictAllow {
		t.Fatalf("应触发硬止损，实际 %s（%s）", decision.Verdict, decision.Reason)
	}

	// 现价 0.9 → 继续持有
	decision, _ = e.CheckExit(pos, 0.90, 50000)
	if decision.Verdict != model.VerdictReject {
		t.Fatalf("不应触发退出，实际 %s", decision.Verdict)
	}
}

func TestCheckExitLiquidityDrop(t *testing.T) {
	e := newTestEngine()
	pos := &model.Position{EntryPriceUSD: 1.0, PeakPriceUSD: 1.0, StopLossPct: 0.25, LiquidityAtEntryUSD: 100000}

	decision, _ := e.CheckExit(pos, 1.0, 60000) // 流动性下降 40% > 35%
	if decision.Verdict != model.VerdictAllow {
		t.Fatalf("流动性骤降应触发退出，实际 %s（%s）", decision.Verdict, decision.Reason)
	}
}

func TestCheckExitTrailingStop(t *testing.T) {
	e := newTestEngine()
	pos := &model.Position{
		EntryPriceUSD: 1.0, PeakPriceUSD: 2.0,
		StopLossPct: 0.25, TrailingStopPct: 0.20, LiquidityAtEntryUSD: 100000,
	}

	// 峰值 2.0 回落 20% → 1.6 触发
	decision, _ := e.CheckExit(pos, 1.59, 100000)
	if decision.Verdict != model.VerdictAllow {
		t.Fatalf("应触发移动止盈，实际 %s（%s）", decision.Verdict, decision.Reason)
	}

	decision, _ = e.CheckExit(pos, 1.8, 100000)
	if decision.Verdict != model.VerdictReject {
		t.Fatalf("未回落到位不应退出，实际 %s", decision.Verdict)
	}
}

func TestRecordLossCreatesCooldown(t *testing.T) {
	e := newTestEngine()
	e.RecordLoss("base", "0xtoken", 50, 30*time.Minute)

	decision, _ := e.CheckEntry(model.EntryRequest{
		Chain: "base", Token: "0xtoken", AmountUSD: 100, LiquidityUSD: 100000,
	})
	if decision.Verdict != model.VerdictReject {
		t.Fatal("亏损冷却期内应拒绝再次入场")
	}
}

func TestMemoryStoreTTL(t *testing.T) {
	store := NewMemoryStore()
	ctx := t.Context()
	if err := store.Set(ctx, "k", "v", 10*time.Millisecond); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, ok, _ := store.Get(ctx, "k"); !ok {
		t.Fatal("未过期时应能读取")
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok, _ := store.Get(ctx, "k"); ok {
		t.Fatal("过期后应读取不到")
	}
}
