package backtest

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"meme-bot/internal/address"
	"meme-bot/internal/config"
	"meme-bot/internal/model"
	"meme-bot/internal/risk"
	"meme-bot/internal/strategy"
)

var baseTime = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

func buildEngine(t *testing.T) (*Engine, *MarketStub) {
	t.Helper()

	store := address.NewMemoryStore()
	if err := store.Upsert(context.Background(), &model.AddressProfile{
		Address: "0xWhaleA", Chain: "base", MedianBuyUSD: 3000, TotalTrades: 30,
		WinRate: 0.7, ProfitFactor: 2.2, MaxDrawdown: 0.2, AvgHoldSeconds: 7200,
		LastActive: baseTime,
	}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}

	scorer := address.NewScorer(config.ScoreConfig{
		MinTrades: 5, MinWinRate: 0.5, MinProfitFactor: 1.5, MaxDrawdown: 0.5, ScoreThreshold: 0.5,
	}, 2.0, store, nil)

	stub := NewMarketStub(100000)
	sigEngine := strategy.NewSignalEngine(config.SignalConfig{
		TTLMinutes: 30, FollowConfirmWindowMinutes: 30, LargeBuyMultiple: 2.0, CooldownMinutes: 0,
	}, model.SignalFilter{RejectHoneypot: true, MinLiquidityUSD: 10000}, stub, scorer, nil, nil, nil)

	riskEngine := risk.New(config.RiskConfig{
		MaxPositionPct: 0.08, MaxOpenPositions: 4, StopLossPct: 0.25, TrailingStopPct: 0.50,
		LiquidityDropTriggerPct: 0.90, MaxSlippageBps: 150, SplitEntries: 1,
	}, nil, nil, nil, nil)
	riskEngine.SetEquity(10000)

	eng := New(DefaultConfig(), sigEngine, riskEngine, nil)
	eng.OnEvent = stub.Observe
	return eng, stub
}

func ev(min int, sender, token string, amount, price float64) model.SwapEvent {
	return model.SwapEvent{
		Chain: "base", TxHash: "0xtx", Pool: "0xpool", TokenIn: "0xETH",
		TokenOut: token, Sender: sender, AmountUSD: amount, PriceUSD: price,
		At: baseTime.Add(time.Duration(min) * time.Minute),
	}
}

func TestReplayProducesWinAndLoss(t *testing.T) {
	eng, _ := buildEngine(t)
	events := []model.SwapEvent{
		// TOKENA：大户买入 → 跟风确认 → 价格跌破止损
		ev(0, "0xWhaleA", "0xTOKENA", 12000, 0.00040),
		ev(2, "0xFollower", "0xTOKENA", 6000, 0.00042),
		ev(30, "0xWhaleA", "0xTOKENA", 2000, 0.00030),
		// TOKENB：大户买入 → 跟风确认 → 价格上涨触发止盈
		ev(40, "0xWhaleA", "0xTOKENB", 15000, 0.00100),
		ev(42, "0xFollower", "0xTOKENB", 5000, 0.00105),
		ev(90, "0xWhaleA", "0xTOKENB", 3000, 0.00190),
	}

	rep, err := eng.Run(context.Background(), NewSliceSource(events))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if rep.Metrics.Events != 6 {
		t.Fatalf("事件数应为 6，实际 %d", rep.Metrics.Events)
	}
	if rep.Metrics.Signals < 2 {
		t.Fatalf("应至少产生 2 个信号，实际 %d", rep.Metrics.Signals)
	}
	if rep.Metrics.Trades != 2 {
		t.Fatalf("应有 2 笔成交，实际 %d：%+v", rep.Metrics.Trades, rep.Trades)
	}
	if rep.Metrics.Wins != 1 || rep.Metrics.Losses != 1 {
		t.Fatalf("应有 1 胜 1 负，实际 %d 胜 %d 负", rep.Metrics.Wins, rep.Metrics.Losses)
	}
	if rep.Metrics.WinRate != 0.5 {
		t.Fatalf("胜率应为 0.5，实际 %v", rep.Metrics.WinRate)
	}
	if rep.Metrics.TotalPnLUSD <= 0 {
		t.Fatalf("总盈亏应为正（止盈幅度大于止损），实际 %v", rep.Metrics.TotalPnLUSD)
	}
	if rep.Metrics.MaxDrawdown <= 0 {
		t.Fatalf("应记录到回撤，实际 %v", rep.Metrics.MaxDrawdown)
	}
	if len(rep.Equity) != len(events) {
		t.Fatalf("权益曲线点数应等于事件数，实际 %d", len(rep.Equity))
	}

	var loss, win *Trade
	for i := range rep.Trades {
		if rep.Trades[i].PnLUSD < 0 {
			loss = &rep.Trades[i]
		} else if rep.Trades[i].PnLUSD > 0 {
			win = &rep.Trades[i]
		}
	}
	if loss == nil || win == nil {
		t.Fatal("应同时存在盈利与亏损成交")
	}
	if !strings.Contains(loss.ExitReason, "止损") {
		t.Fatalf("亏损笔应由止损触发，实际原因：%s", loss.ExitReason)
	}
	if !strings.Contains(win.ExitReason, "止盈") {
		t.Fatalf("盈利笔应由止盈触发，实际原因：%s", win.ExitReason)
	}
	if loss.FeesUSD <= 0 {
		t.Fatal("应计入手续费")
	}
	if loss.HoldingMin <= 0 {
		t.Fatal("应记录持仓时长")
	}
	if loss.AmountUSD != win.AmountUSD {
		t.Fatalf("两笔仓位应受同一风控上限约束：%v vs %v", loss.AmountUSD, win.AmountUSD)
	}
}

func TestReplayWithoutFollowerNoTrade(t *testing.T) {
	eng, _ := buildEngine(t)
	// 只有大户单方买入（窗口内无第二个独立买家）→ 不应入场
	rep, err := eng.Run(context.Background(), NewSliceSource([]model.SwapEvent{
		ev(0, "0xWhaleA", "0xTOKENA", 12000, 0.00040),
		ev(5, "0xWhaleA", "0xTOKENA", 12000, 0.00045),
		ev(10, "0xWhaleA", "0xTOKENA", 12000, 0.00050),
	}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Metrics.Trades != 0 {
		t.Fatalf("无跟风确认时不应成交，实际 %d 笔", rep.Metrics.Trades)
	}
}

func TestReplaySignalExpiresWithoutFollower(t *testing.T) {
	eng, _ := buildEngine(t)
	// 第二笔买家出现在 TTL（30min）之后 → 信号已过期，不应成交
	rep, err := eng.Run(context.Background(), NewSliceSource([]model.SwapEvent{
		ev(0, "0xWhaleA", "0xTOKENA", 12000, 0.00040),
		ev(45, "0xFollower", "0xTOKENA", 6000, 0.00045),
	}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Metrics.Trades != 0 {
		t.Fatalf("信号过期后不应成交，实际 %d 笔", rep.Metrics.Trades)
	}
}

func TestJSONLSourceRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := SampleJSONL(&buf); err != nil {
		t.Fatalf("sample: %v", err)
	}

	src, err := LoadJSONLReader(&buf)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if src.Len() != 4 {
		t.Fatalf("示例应有 4 条事件，实际 %d", src.Len())
	}

	prev := time.Time{}
	count := 0
	for {
		event, ok, err := src.Next()
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		if !ok {
			break
		}
		count++
		if event.TokenOut == "" || event.Chain == "" {
			t.Fatalf("事件字段缺失：%+v", event)
		}
		if event.At.Before(prev) {
			t.Fatal("事件应按时间升序")
		}
		prev = event.At
	}
	if count != 4 {
		t.Fatalf("应遍历 4 条事件，实际 %d", count)
	}
}

func TestLoadJSONLRejectsBadInput(t *testing.T) {
	lf := string(rune(10))
	if _, err := LoadJSONLReader(strings.NewReader("{not-json}" + lf)); err == nil {
		t.Fatal("非法 JSON 行应报错")
	}
	if _, err := LoadJSONLReader(strings.NewReader("# 只有注释" + lf)); err == nil {
		t.Fatal("无有效事件应报错")
	}
	if _, err := LoadJSONLReader(strings.NewReader("")); err == nil {
		t.Fatal("空输入应报错")
	}
}

func TestMarketStubProvidesData(t *testing.T) {
	stub := NewMarketStub(0) // 使用默认基线
	stub.Observe(model.SwapEvent{Chain: "base", TokenOut: "0xT", AmountUSD: 5000, PriceUSD: 0.001})

	price, err := stub.TokenPrice(context.Background(), "base", "0xT")
	if err != nil || price.USD != 0.001 {
		t.Fatalf("价格应由事件驱动：%+v err=%v", price, err)
	}
	liq, err := stub.Liquidity(context.Background(), "base", "0xT")
	if err != nil || liq.LiquidityUSD <= 0 {
		t.Fatalf("流动性应为正值：%+v err=%v", liq, err)
	}
	sec, err := stub.Security(context.Background(), "base", "0xT")
	if err != nil || sec.Risky {
		t.Fatalf("回测桩的安全报告应通过：%+v err=%v", sec, err)
	}
}
