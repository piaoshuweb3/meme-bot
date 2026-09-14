package strategy

import (
	"context"
	"errors"
	"testing"
	"time"

	"meme-bot/internal/config"
	"meme-bot/internal/market"
	"meme-bot/internal/model"
)

// ---- 测试替身 -----------------------------------------------------------------

type fakeMarket struct {
	security    *model.SecurityReport
	securityErr error
	liq         *model.LiquidityInfo
	liqErr      error
	secCalls    int
	liqCalls    int
}

func (f *fakeMarket) TokenPrice(context.Context, string, string) (*model.Price, error) {
	return &model.Price{USD: 1}, nil
}

func (f *fakeMarket) Liquidity(context.Context, string, string) (*model.LiquidityInfo, error) {
	f.liqCalls++
	if f.liqErr != nil {
		return nil, f.liqErr
	}
	return f.liq, nil
}

func (f *fakeMarket) Security(context.Context, string, string) (*model.SecurityReport, error) {
	f.secCalls++
	if f.securityErr != nil {
		return nil, f.securityErr
	}
	return f.security, nil
}

func (f *fakeMarket) Concentration(context.Context, string, string, int) (*model.Concentration, error) {
	return &model.Concentration{}, nil
}

func (f *fakeMarket) Swaps(context.Context, string, string, func(model.SwapEvent)) (model.Unsubscribe, error) {
	return func() {}, nil
}

type fakeScorer struct {
	large bool
	err   error
}

func (f *fakeScorer) Score(*model.AddressProfile) float64 { return 0.5 }

func (f *fakeScorer) IsEligible(context.Context, string, string) (bool, float64, error) {
	return true, 0.5, nil
}

func (f *fakeScorer) IsLargeBuy(context.Context, string, string, float64) (bool, error) {
	return f.large, f.err
}

type fakeAlert struct{ raised int }

func (f *fakeAlert) Raise(context.Context, *model.Alert) error {
	f.raised++
	return nil
}

var testFixedNow = time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)

func strictFilter() model.SignalFilter {
	return model.SignalFilter{
		MinLiquidityUSD:     1000,
		MaxBuyTax:           0.1,
		MaxSellTax:          0.1,
		RequireOpenSource:   true,
		RejectHoneypot:      true,
		RejectMintable:      true,
		RejectWithBlacklist: true,
	}
}

func goodSecurity() *model.SecurityReport {
	return &model.SecurityReport{Chain: "base", Token: "T", IsOpenSource: true, BuyTax: 0.01, SellTax: 0.01}
}

func goodLiquidity() *model.LiquidityInfo {
	return &model.LiquidityInfo{Chain: "base", Token: "T", LiquidityUSD: 50000, Volume24hUSD: 150000}
}

func newEngine(mkt model.MarketPort, sc model.ScorerPort, filter model.SignalFilter, cfg config.SignalConfig) *SignalEngine {
	return NewSignalEngine(cfg, filter, mkt, sc, &fakeAlert{}, nil, nil).
		WithClock(func() time.Time { return testFixedNow })
}

func buyEvent() model.SwapEvent {
	return model.SwapEvent{
		Chain: "base", TxHash: "0x1", TokenIn: "USDC", TokenOut: "T",
		Sender: "0xwhale", AmountUSD: 5000, PriceUSD: 0.5, At: testFixedNow,
	}
}

// ---- Evaluate 漏斗 ------------------------------------------------------------

func TestEvaluateIgnoresNonBuyEvents(t *testing.T) {
	eng := newEngine(&fakeMarket{security: goodSecurity(), liq: goodLiquidity()}, &fakeScorer{large: true}, strictFilter(), config.SignalConfig{})

	if sig, err := eng.Evaluate(model.SwapEvent{Chain: "base", AmountUSD: 100}); err != nil || sig != nil {
		t.Fatalf("无 TokenOut 应忽略：sig=%v err=%v", sig, err)
	}
	ev := buyEvent()
	ev.AmountUSD = 0
	if sig, err := eng.Evaluate(ev); err != nil || sig != nil {
		t.Fatalf("金额为 0 应忽略：sig=%v err=%v", sig, err)
	}
}

func TestEvaluateRejectsHoneypot(t *testing.T) {
	sec := goodSecurity()
	sec.IsHoneypot = true
	mkt := &fakeMarket{security: sec, liq: goodLiquidity()}
	eng := newEngine(mkt, &fakeScorer{large: true}, strictFilter(), config.SignalConfig{})

	sig, err := eng.Evaluate(buyEvent())
	if err != nil {
		t.Fatalf("不应报错：%v", err)
	}
	if sig != nil {
		t.Fatalf("蜜罐代币不应生成信号：%+v", sig)
	}
	if mkt.liqCalls != 0 {
		t.Fatal("安全过滤应在流动性查询之前短路")
	}
}

func TestEvaluateRejectsNilSecurityWhenConservative(t *testing.T) {
	eng := newEngine(&fakeMarket{security: nil, liq: goodLiquidity()}, &fakeScorer{large: true}, strictFilter(), config.SignalConfig{})
	if sig, err := eng.Evaluate(buyEvent()); err != nil || sig != nil {
		t.Fatalf("保守模式下无安全报告应拒绝：sig=%v err=%v", sig, err)
	}

	filter := strictFilter()
	filter.RejectHoneypot = false
	eng2 := newEngine(&fakeMarket{security: nil, liq: goodLiquidity()}, &fakeScorer{large: true}, filter, config.SignalConfig{})
	sig, err := eng2.Evaluate(buyEvent())
	if err != nil || sig == nil {
		t.Fatalf("非保守模式应放行：sig=%v err=%v", sig, err)
	}
}

func TestEvaluateSecurityErrorIsSwallowed(t *testing.T) {
	eng := newEngine(&fakeMarket{securityErr: errors.New("rpc down"), liq: goodLiquidity()}, &fakeScorer{large: true}, strictFilter(), config.SignalConfig{})
	if sig, err := eng.Evaluate(buyEvent()); err != nil || sig != nil {
		t.Fatalf("安全查询失败应静默跳过（不阻断管道）：sig=%v err=%v", sig, err)
	}
}

func TestEvaluateRejectsLowLiquidity(t *testing.T) {
	liq := goodLiquidity()
	liq.LiquidityUSD = 500
	eng := newEngine(&fakeMarket{security: goodSecurity(), liq: liq}, &fakeScorer{large: true}, strictFilter(), config.SignalConfig{})
	if sig, err := eng.Evaluate(buyEvent()); err != nil || sig != nil {
		t.Fatalf("流动性不足应拒绝：sig=%v err=%v", sig, err)
	}
}

func TestEvaluateGeneratesSignalOnLargeBuy(t *testing.T) {
	eng := newEngine(&fakeMarket{security: goodSecurity(), liq: goodLiquidity()}, &fakeScorer{large: true},
		strictFilter(), config.SignalConfig{TTLMinutes: 30, CooldownMinutes: 10})

	sig, err := eng.Evaluate(buyEvent())
	if err != nil {
		t.Fatalf("Evaluate 失败：%v", err)
	}
	if sig == nil {
		t.Fatal("大额买入应生成信号")
	}
	if sig.Status != model.SignalPending || sig.Decay != 1.0 {
		t.Fatalf("初始状态错误：status=%v decay=%v", sig.Status, sig.Decay)
	}
	if sig.Token != "T" || sig.TriggerAddress != "0xwhale" || sig.Chain != "base" {
		t.Fatalf("字段错误：%+v", sig)
	}
	if sig.LiquidityUSD != 50000 || sig.AmountUSD != 5000 {
		t.Fatalf("金额字段错误：%+v", sig)
	}
	if want := testFixedNow.Add(30 * time.Minute); !sig.ExpiresAt.Equal(want) {
		t.Fatalf("TTL 错误：got %v want %v", sig.ExpiresAt, want)
	}
	if sig.VolumeSpike != 3 {
		t.Fatalf("成交额倍数回退口径应为 150000/50000=3：%v", sig.VolumeSpike)
	}
	if n := len(eng.Active()); n != 1 {
		t.Fatalf("应进入活跃集合：%d", n)
	}
}

func TestEvaluateNonLargeBuyOnlyTracksBuyer(t *testing.T) {
	eng := newEngine(&fakeMarket{security: goodSecurity(), liq: goodLiquidity()}, &fakeScorer{large: false},
		strictFilter(), config.SignalConfig{})

	sig, err := eng.Evaluate(buyEvent())
	if err != nil || sig != nil {
		t.Fatalf("非大额买入不应生成信号：sig=%v err=%v", sig, err)
	}
	if n := len(eng.Active()); n != 0 {
		t.Fatalf("活跃集合应为空：%d", n)
	}
	eng.mu.Lock()
	buyers := len(eng.buyers["T"])
	eng.mu.Unlock()
	if buyers != 1 {
		t.Fatalf("非大额买入也应记录买家用于跟风确认：%d", buyers)
	}
}

func TestEvaluateLargeBuyCheckErrorTreatedAsNotLarge(t *testing.T) {
	eng := newEngine(&fakeMarket{security: goodSecurity(), liq: goodLiquidity()}, &fakeScorer{err: errors.New("db down")},
		strictFilter(), config.SignalConfig{})
	if sig, err := eng.Evaluate(buyEvent()); err != nil || sig != nil {
		t.Fatalf("大额判定失败时应退化为不生成信号：sig=%v err=%v", sig, err)
	}
}

func TestEvaluateCooldownSuppressesRepeatSignal(t *testing.T) {
	eng := newEngine(&fakeMarket{security: goodSecurity(), liq: goodLiquidity()}, &fakeScorer{large: true},
		strictFilter(), config.SignalConfig{CooldownMinutes: 30})

	if sig, _ := eng.Evaluate(buyEvent()); sig == nil {
		t.Fatal("首次应生成信号")
	}
	sig, err := eng.Evaluate(buyEvent())
	if err != nil {
		t.Fatalf("Evaluate 失败：%v", err)
	}
	if sig != nil {
		t.Fatalf("冷却窗口内不应重复生成：%+v", sig)
	}
}

func TestSecurityPassMatrix(t *testing.T) {
	eng := &SignalEngine{filter: strictFilter()}

	cases := []struct {
		name     string
		report   *model.SecurityReport
		wantPass bool
	}{
		{"正常合约", goodSecurity(), true},
		{"无报告-保守拒绝", nil, false},
		{"蜜罐", &model.SecurityReport{IsHoneypot: true, IsOpenSource: true}, false},
		{"可增发", &model.SecurityReport{HasMint: true, IsOpenSource: true}, false},
		{"有黑名单", &model.SecurityReport{HasBlacklist: true, IsOpenSource: true}, false},
		{"未开源", &model.SecurityReport{IsOpenSource: false}, false},
		{"买入税过高", &model.SecurityReport{IsOpenSource: true, BuyTax: 0.5}, false},
		{"卖出税过高", &model.SecurityReport{IsOpenSource: true, SellTax: 0.5}, false},
	}
	for _, c := range cases {
		pass, reason := eng.securityPass(c.report)
		if pass != c.wantPass {
			t.Fatalf("%s: pass=%v want=%v (reason=%s)", c.name, pass, c.wantPass, reason)
		}
	}
}

func TestVolumeMultipleFallback(t *testing.T) {
	eng := &SignalEngine{}
	if got := eng.volumeMultiple(nil); got != 0 {
		t.Fatalf("nil 流动性应为 0：%v", got)
	}
	if got := eng.volumeMultiple(&model.LiquidityInfo{LiquidityUSD: 0, Volume24hUSD: 100}); got != 0 {
		t.Fatalf("流动性为 0 应为 0（避免除零）：%v", got)
	}
	if got := eng.volumeMultiple(&model.LiquidityInfo{LiquidityUSD: 1000, Volume24hUSD: 2500}); got != 2.5 {
		t.Fatalf("比值口径错误：%v", got)
	}
}

func TestVolumeMultipleForUsesRollingWindowWhenSamplesEnough(t *testing.T) {
	w := market.NewRollingWindow(time.Hour, 64)
	eng := newEngine(&fakeMarket{security: goodSecurity(), liq: goodLiquidity()}, &fakeScorer{large: true},
		strictFilter(), config.SignalConfig{}).WithRolling(w)

	liq := &model.LiquidityInfo{Chain: "base", Token: "T", LiquidityUSD: 1000, Volume24hUSD: 100}
	for i := 0; i < 3; i++ {
		if got := eng.volumeMultipleFor(liq, "base", "T", testFixedNow.Add(time.Duration(i)*time.Minute)); got != 0 {
			t.Fatalf("样本不足时应返回 0（暂不判定）：%v", got)
		}
	}
	spike := &model.LiquidityInfo{Chain: "base", Token: "T", LiquidityUSD: 1000, Volume24hUSD: 600}
	if got := eng.volumeMultipleFor(spike, "base", "T", testFixedNow.Add(5*time.Minute)); got != 6 {
		t.Fatalf("相对自身均值的倍数应为 6：%v", got)
	}
	if got := eng.volumeMultipleFor(&model.LiquidityInfo{}, "base", "T", testFixedNow); got != 0 {
		t.Fatalf("无成交量应为 0：%v", got)
	}
	if got := eng.volumeMultipleFor(nil, "base", "T", testFixedNow); got != 0 {
		t.Fatalf("nil 流动性应为 0：%v", got)
	}
}

func TestWithNilDependenciesDoNotOverride(t *testing.T) {
	eng := newEngine(&fakeMarket{security: goodSecurity(), liq: goodLiquidity()}, &fakeScorer{large: true}, strictFilter(), config.SignalConfig{})
	eng.WithRolling(nil).WithClock(nil)
	if eng.rolling != nil {
		t.Fatal("WithRolling(nil) 不应覆盖既有窗口")
	}
	if got := eng.now(); !got.Equal(testFixedNow) {
		t.Fatalf("WithClock(nil) 不应覆盖已注入时钟：%v", got)
	}
}

// ---- Confirm / Decay --------------------------------------------------------

func TestConfirmNilSignal(t *testing.T) {
	eng := newEngine(&fakeMarket{}, &fakeScorer{}, strictFilter(), config.SignalConfig{})
	if _, err := eng.Confirm(nil); err == nil {
		t.Fatal("nil 信号应报错")
	}
}

func TestConfirmExpiredMarksExpired(t *testing.T) {
	eng := newEngine(&fakeMarket{}, &fakeScorer{}, strictFilter(), config.SignalConfig{TTLMinutes: 30})
	sig := &model.Signal{ID: "s1", Token: "T", Status: model.SignalPending,
		CreatedAt: testFixedNow.Add(-2 * time.Hour), ExpiresAt: testFixedNow.Add(-time.Hour)}

	got, err := eng.Confirm(sig)
	if err != nil {
		t.Fatalf("Confirm 失败：%v", err)
	}
	if got.Status != model.SignalExpired || got.Decay != 0 {
		t.Fatalf("过期信号应标记 Expired 且衰减为 0：%+v", got)
	}
}

func TestConfirmFollowConfirmedNeedsTwoDistinctBuyers(t *testing.T) {
	eng := newEngine(&fakeMarket{}, &fakeScorer{}, strictFilter(), config.SignalConfig{FollowConfirmWindowMinutes: 30})

	eng.trackBuyer("T", "0xa", testFixedNow)
	sig := &model.Signal{ID: "s1", Token: "T", Status: model.SignalPending,
		CreatedAt: testFixedNow, ExpiresAt: testFixedNow.Add(time.Hour)}
	got, err := eng.Confirm(sig)
	if err != nil {
		t.Fatalf("Confirm 失败：%v", err)
	}
	if got.FollowConfirmed || got.Status == model.SignalConfirmed {
		t.Fatalf("单个买家不应确认跟风：%+v", got)
	}

	eng.trackBuyer("T", "0xb", testFixedNow)
	got, _ = eng.Confirm(sig)
	if !got.FollowConfirmed || got.Status != model.SignalConfirmed || got.ConfirmedAt.IsZero() {
		t.Fatalf("两个独立买家应确认跟风：%+v", got)
	}
}

func TestConfirmIgnoresBuyersOutsideWindow(t *testing.T) {
	eng := newEngine(&fakeMarket{}, &fakeScorer{}, strictFilter(), config.SignalConfig{FollowConfirmWindowMinutes: 10})
	old := testFixedNow.Add(-2 * time.Hour)
	eng.trackBuyer("T", "0xa", old)
	eng.trackBuyer("T", "0xb", old)

	sig := &model.Signal{ID: "s1", Token: "T", ExpiresAt: testFixedNow.Add(time.Hour)}
	got, _ := eng.Confirm(sig)
	if got.FollowConfirmed {
		t.Fatal("窗口外的买家不应计入跟风确认")
	}
}

func TestDecayExpiresAndPartiallyDecays(t *testing.T) {
	eng := newEngine(&fakeMarket{security: goodSecurity(), liq: goodLiquidity()}, &fakeScorer{large: true},
		strictFilter(), config.SignalConfig{TTLMinutes: 60})
	if sig, _ := eng.Evaluate(buyEvent()); sig == nil {
		t.Fatal("应先生成信号")
	}

	half := eng.Decay(testFixedNow.Add(30 * time.Minute))
	if len(half) != 0 {
		t.Fatalf("未到期不应出现在过期列表：%d", len(half))
	}
	active := eng.Active()
	if len(active) != 1 {
		t.Fatalf("信号应仍活跃：%d", len(active))
	}
	if active[0].Decay < 0.4 || active[0].Decay > 0.6 {
		t.Fatalf("衰减比例应约为 0.5：%v", active[0].Decay)
	}

	expired := eng.Decay(testFixedNow.Add(61 * time.Minute))
	if len(expired) != 1 || expired[0].Status != model.SignalExpired || expired[0].Decay != 0 {
		t.Fatalf("应返回一个过期信号：%+v", expired)
	}
	if n := len(eng.Active()); n != 0 {
		t.Fatalf("过期信号应移出活跃集合：%d", n)
	}
}

func TestDecayDropsSignalsWithInvalidWindow(t *testing.T) {
	eng := newEngine(&fakeMarket{}, &fakeScorer{}, strictFilter(), config.SignalConfig{})
	eng.mu.Lock()
	eng.active["bad"] = &model.Signal{ID: "bad", CreatedAt: testFixedNow, ExpiresAt: testFixedNow}
	eng.mu.Unlock()

	_ = eng.Decay(testFixedNow)
	if n := len(eng.Active()); n != 0 {
		t.Fatalf("TTL 无效的信号应被丢弃：%d", n)
	}
}

func TestTrackBuyerIgnoresEmptyBuyer(t *testing.T) {
	eng := newEngine(&fakeMarket{}, &fakeScorer{}, strictFilter(), config.SignalConfig{})
	eng.trackBuyer("T", "   ", testFixedNow)
	eng.mu.Lock()
	n := len(eng.buyers["T"])
	eng.mu.Unlock()
	if n != 0 {
		t.Fatalf("空买家不应记录：%d", n)
	}
}

func TestMinHelper(t *testing.T) {
	if min(3, 5) != 3 || min(5, 3) != 3 || min(4, 4) != 4 {
		t.Fatal("min 实现有误")
	}
}
