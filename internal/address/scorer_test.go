package address

import (
	"testing"
	"time"

	"meme-bot/internal/config"
	"meme-bot/internal/model"
)

func TestScreenRejectsBlacklistedAndBadTags(t *testing.T) {
	cfg := config.ScoreConfig{MinWinRate: 0.55, MinProfitFactor: 1.8, MaxDrawdown: 0.40, MinTrades: 20}

	good := &model.AddressProfile{
		Address: "0xabc", Chain: "base", WinRate: 0.62, ProfitFactor: 2.2,
		MaxDrawdown: 0.25, TotalTrades: 40, AvgHoldSeconds: 3600,
	}
	if res := Screen(good, cfg); !res.Passed {
		t.Fatalf("正常画像不应被拒绝，理由：%s", res.Reason)
	}

	black := *good
	black.IsBlacklisted = true
	black.BlacklistReason = "历史 Rug"
	if res := Screen(&black, cfg); res.Passed {
		t.Fatal("黑名单地址必须被拒绝")
	}

	bot := *good
	bot.Tags = []model.AddressTag{model.TagBot}
	if res := Screen(&bot, cfg); res.Passed {
		t.Fatal("机器人标签必须被拒绝")
	}

	lowWin := *good
	lowWin.WinRate = 0.30
	if res := Screen(&lowWin, cfg); res.Passed {
		t.Fatal("胜率不达标必须被拒绝")
	}

	fewTrades := *good
	fewTrades.TotalTrades = 3
	if res := Screen(&fewTrades, cfg); res.Passed {
		t.Fatal("交易样本不足必须被拒绝")
	}
}

func TestScorerWeightsSumToOne(t *testing.T) {
	s := NewScorer(config.ScoreConfig{}, 2.0, NewMemoryStore(), nil)
	w := s.weights()
	var sum float64
	for _, v := range w {
		sum += v
	}
	if sum < 0.999 || sum > 1.001 {
		t.Fatalf("权重归一化失败：sum=%f", sum)
	}
}

func TestScoreIsBoundedAndMonotonic(t *testing.T) {
	s := NewScorer(config.ScoreConfig{}, 2.0, NewMemoryStore(), nil)

	now := time.Now().UTC()
	weak := &model.AddressProfile{
		WinRate: 0.3, ProfitFactor: 0.8, MaxDrawdown: 0.8, Consistency: 0.2, LastActive: now.Add(-60 * 24 * time.Hour),
	}
	strong := &model.AddressProfile{
		WinRate: 0.75, ProfitFactor: 3.0, MaxDrawdown: 0.1, Consistency: 0.9, LastActive: now,
	}

	ws, ss := s.Score(weak), s.Score(strong)
	if ws < 0 || ws > 1 || ss < 0 || ss > 1 {
		t.Fatalf("评分必须落在 [0,1]：weak=%f strong=%f", ws, ss)
	}
	if ss <= ws {
		t.Fatalf("优秀地址评分应高于劣质地址：weak=%f strong=%f", ws, ss)
	}
}

func TestIsEligibleUsesThreshold(t *testing.T) {
	store := NewMemoryStore()
	cfg := config.ScoreConfig{
		MinWinRate: 0.55, MinProfitFactor: 1.8, MaxDrawdown: 0.40, MinTrades: 5,
		ScoreThreshold: 0.70, WindowDays: 90,
	}
	s := NewScorer(cfg, 2.0, store, nil)

	profile := &model.AddressProfile{
		Address: "0xdead", Chain: "base", WinRate: 0.72, ProfitFactor: 2.6,
		MaxDrawdown: 0.12, Consistency: 0.8, TotalTrades: 30,
		AvgHoldSeconds: 7200, LastActive: time.Now().UTC(),
	}
	if err := store.Upsert(t.Context(), profile); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	ok, score, err := s.IsEligible(t.Context(), "base", "0xdead")
	if err != nil {
		t.Fatalf("IsEligible: %v", err)
	}
	if !ok {
		t.Fatalf("优质地址应可跟单，score=%f", score)
	}
}

func TestComputeStatsFromTrades(t *testing.T) {
	base := time.Now().UTC().Add(-10 * time.Hour)
	trades := []*model.TradeRecord{
		{Chain: "base", Address: "0x1", Token: "T", Side: model.SideBuy, AmountUSD: 100, At: base},
		{Chain: "base", Address: "0x1", Token: "T", Side: model.SideSell, AmountUSD: 150, PnLUSD: 50, At: base.Add(time.Hour)},
		{Chain: "base", Address: "0x1", Token: "U", Side: model.SideBuy, AmountUSD: 200, At: base.Add(2 * time.Hour)},
		{Chain: "base", Address: "0x1", Token: "U", Side: model.SideSell, AmountUSD: 150, PnLUSD: -50, At: base.Add(3 * time.Hour)},
	}

	st := computeStats(trades)
	if st.wins != 1 {
		t.Fatalf("盈利交易数应为 1，实际 %d", st.wins)
	}
	if st.winRate < 0.49 || st.winRate > 0.51 {
		t.Fatalf("胜率应为 0.5，实际 %f", st.winRate)
	}
	if st.profitFactor != 1 {
		t.Fatalf("盈亏比应为 1，实际 %f", st.profitFactor)
	}
	if st.medianBuyUSD != 150 {
		t.Fatalf("买入金额中位数应为 150，实际 %f", st.medianBuyUSD)
	}
	if st.avgHoldSeconds != 3600 {
		t.Fatalf("平均持仓时长应为 3600s，实际 %d", st.avgHoldSeconds)
	}
	if st.maxDrawdown <= 0 {
		t.Fatal("应检测到回撤")
	}
}

func TestIsLargeBuyUsesMedian(t *testing.T) {
	store := NewMemoryStore()
	cfg := config.ScoreConfig{MinTrades: 1, ScoreThreshold: 0.5}
	s := NewScorer(cfg, 2.0, store, nil)

	if err := store.Upsert(t.Context(), &model.AddressProfile{
		Address: "0xwhale", Chain: "base", MedianBuyUSD: 1000, TotalTrades: 10,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	large, err := s.IsLargeBuy(t.Context(), "base", "0xwhale", 2500)
	if err != nil {
		t.Fatalf("IsLargeBuy: %v", err)
	}
	if !large {
		t.Fatal("2500 应为大额（中位数 1000 × 2）")
	}

	small, err := s.IsLargeBuy(t.Context(), "base", "0xwhale", 1200)
	if err != nil {
		t.Fatalf("IsLargeBuy: %v", err)
	}
	if small {
		t.Fatal("1200 不应判定为大额")
	}
}
