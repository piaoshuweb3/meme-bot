package chain

import (
	"context"
	"fmt"

	"meme-bot/internal/model"
)

// Market 把 Factory 收敛为 model.MarketPort：策略层只依赖接口，不感知具体链。
type Market struct {
	f *Factory
}

// NewMarket 构建多链行情聚合器。
func NewMarket(f *Factory) *Market { return &Market{f: f} }

func (m *Market) adapter(chainID string) (model.ChainAdapter, error) {
	if m == nil || m.f == nil {
		return nil, fmt.Errorf("market: nil factory")
	}
	return m.f.Get(chainID)
}

// TokenPrice 实现 model.MarketPort。
func (m *Market) TokenPrice(ctx context.Context, chainID, token string) (*model.Price, error) {
	a, err := m.adapter(chainID)
	if err != nil {
		return nil, err
	}
	return a.TokenPrice(ctx, token)
}

// Liquidity 实现 model.MarketPort。
func (m *Market) Liquidity(ctx context.Context, chainID, token string) (*model.LiquidityInfo, error) {
	a, err := m.adapter(chainID)
	if err != nil {
		return nil, err
	}
	return a.Liquidity(ctx, token)
}

// Security 实现 model.MarketPort。
func (m *Market) Security(ctx context.Context, chainID, token string) (*model.SecurityReport, error) {
	a, err := m.adapter(chainID)
	if err != nil {
		return nil, err
	}
	return a.TokenSecurity(ctx, token)
}

// Concentration 实现 model.MarketPort。
func (m *Market) Concentration(ctx context.Context, chainID, token string, topN int) (*model.Concentration, error) {
	a, err := m.adapter(chainID)
	if err != nil {
		return nil, err
	}
	return a.HolderConcentration(ctx, token, topN)
}

// Swaps 实现 model.MarketPort。
func (m *Market) Swaps(ctx context.Context, chainID, token string, handler func(model.SwapEvent)) (model.Unsubscribe, error) {
	a, err := m.adapter(chainID)
	if err != nil {
		return nil, err
	}
	return a.SubscribeSwaps(ctx, token, handler)
}

// 编译期断言：Market 满足 model.MarketPort。
var _ model.MarketPort = (*Market)(nil)
