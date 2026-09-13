package backtest

import (
	"context"
	"sync"

	"meme-bot/internal/model"
)

// MarketStub 回测专用行情桩：由事件驱动维护价格与流动性。
//
// 目的：SignalEngine.Evaluate 需要行情与安全数据，而回测不做外部请求。
// 约定：安全报告恒为"通过"（回测聚焦信号漏斗与风控，而非安全数据源本身），
// 流动性取"事件成交额 × 倍数"与基线的较大值，避免因数据缺失被门槛过滤。
type MarketStub struct {
	mu                   sync.Mutex
	baselineLiquidityUSD float64
	liquidityMultiple    float64
	liquidity            map[string]float64
	price                map[string]float64
}

// NewMarketStub 构建行情桩（baselineLiquidityUSD 为最低流动性基线）。
func NewMarketStub(baselineLiquidityUSD float64) *MarketStub {
	if baselineLiquidityUSD <= 0 {
		baselineLiquidityUSD = 100000
	}
	return &MarketStub{
		baselineLiquidityUSD: baselineLiquidityUSD,
		liquidityMultiple:    20,
		liquidity:            make(map[string]float64),
		price:                make(map[string]float64),
	}
}

func key(chain, token string) string { return chain + "|" + token }

// Observe 用事件更新该代币的价格与流动性估计。
func (m *MarketStub) Observe(ev model.SwapEvent) {
	if ev.TokenOut == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ev.PriceUSD > 0 {
		m.price[key(ev.Chain, ev.TokenOut)] = ev.PriceUSD
	}
	if ev.AmountUSD > 0 {
		est := ev.AmountUSD * m.liquidityMultiple
		if est < m.baselineLiquidityUSD {
			est = m.baselineLiquidityUSD
		}
		m.liquidity[key(ev.Chain, ev.TokenOut)] = est
	}
}

// TokenPrice 实现 model.MarketPort。
func (m *MarketStub) TokenPrice(_ context.Context, chain, token string) (*model.Price, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &model.Price{Chain: chain, Token: token, USD: m.price[key(chain, token)], Source: "backtest"}, nil
}

// Liquidity 实现 model.MarketPort。
func (m *MarketStub) Liquidity(_ context.Context, chain, token string) (*model.LiquidityInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	liq := m.liquidity[key(chain, token)]
	if liq <= 0 {
		liq = m.baselineLiquidityUSD
	}
	return &model.LiquidityInfo{
		Chain: chain, Token: token, LiquidityUSD: liq,
		Volume24hUSD: liq * 0.5, PoolAddress: "backtest-pool",
	}, nil
}

// Security 实现 model.MarketPort：回测中恒为通过（详见类型注释）。
func (m *MarketStub) Security(_ context.Context, chain, token string) (*model.SecurityReport, error) {
	return &model.SecurityReport{
		Chain: chain, Token: token, Source: "backtest-stub",
		IsOpenSource: true, OwnershipRenounced: true, Risky: false,
	}, nil
}

// Concentration 实现 model.MarketPort。
func (m *MarketStub) Concentration(_ context.Context, chain, token string, _ int) (*model.Concentration, error) {
	return &model.Concentration{Chain: chain, Token: token, Top10Percent: 0.2, Top20Percent: 0.35, HolderCount: 500}, nil
}

// Swaps 实现 model.MarketPort：回测不使用实时订阅。
func (m *MarketStub) Swaps(_ context.Context, _, _ string, _ func(model.SwapEvent)) (model.Unsubscribe, error) {
	return func() {}, nil
}

var _ model.MarketPort = (*MarketStub)(nil)
