package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Market 实现链适配器的 MarketSource：价格与流动性来自 DexScreener（免费、多链）。
//
// 可扩展：当配置了 Birdeye API Key 时，可通过 MultiMarket 优先使用 Birdeye。
type Market struct {
	http    *http.Client
	cache   *cache
	baseURL string
}

// NewMarket 构建行情服务。
func NewMarket() *Market {
	return &Market{
		http:    &http.Client{Timeout: 12 * time.Second},
		cache:   newCache(30 * time.Second),
		baseURL: "https://api.dexscreener.com/latest/dex",
	}
}

type dexPair struct {
	ChainID     string `json:"chainId"`
	DexID       string `json:"dexId"`
	PairAddress string `json:"pairAddress"`
	PriceUSD    string `json:"priceUsd"`
	Volume      struct {
		H24 float64 `json:"h24"`
	} `json:"volume"`
	Liquidity struct {
		USD float64 `json:"usd"`
	} `json:"liquidity"`
	BaseToken struct {
		Address string `json:"address"`
		Symbol  string `json:"symbol"`
	} `json:"baseToken"`
	QuoteToken struct {
		Address string `json:"address"`
		Symbol  string `json:"symbol"`
	} `json:"quoteToken"`
}

type dexResponse struct {
	Pairs []dexPair `json:"pairs"`
}

// bestPair 选择流动性最高的交易对（迷你币常有多个池子）。
func (m *Market) bestPair(ctx context.Context, chainID, token string) (*dexPair, error) {
	key := "pair:" + chainID + ":" + token
	if v, ok := m.cache.get(key); ok {
		if p, ok := v.(*dexPair); ok {
			return p, nil
		}
	}

	var out dexResponse
	endpoint := fmt.Sprintf("%s/tokens/%s", m.baseURL, token)
	if err := httpGetJSON(ctx, m.http, endpoint, nil, &out); err != nil {
		return nil, err
	}

	wantChain := chainIDForAPI(chainID)
	var best *dexPair
	for i := range out.Pairs {
		p := &out.Pairs[i]
		if wantChain != "" && !strings.EqualFold(p.ChainID, wantChain) {
			continue
		}
		if best == nil || p.Liquidity.USD > best.Liquidity.USD {
			best = p
		}
	}
	if best == nil {
		if len(out.Pairs) == 0 {
			return nil, fmt.Errorf("dexscreener: no pair for %s on %s", token, chainID)
		}
		// 没有匹配链的记录时，退化为整体流动性最高的对
		best = &out.Pairs[0]
		for i := range out.Pairs {
			if out.Pairs[i].Liquidity.USD > best.Liquidity.USD {
				best = &out.Pairs[i]
			}
		}
	}
	m.cache.put(key, best)
	return best, nil
}

// PriceUSD 实现 MarketSource。
func (m *Market) PriceUSD(ctx context.Context, chainID, token string) (float64, string, error) {
	p, err := m.bestPair(ctx, chainID, token)
	if err != nil {
		return 0, "", err
	}
	var price float64
	if _, err := fmt.Sscanf(strings.TrimSpace(p.PriceUSD), "%f", &price); err != nil {
		return 0, "", fmt.Errorf("dexscreener: invalid price %q", p.PriceUSD)
	}
	return price, "dexscreener:" + p.DexID, nil
}

// LiquidityUSD 实现 MarketSource：返回 (流动性, 24h 成交量, 池子地址)。
func (m *Market) LiquidityUSD(ctx context.Context, chainID, token string) (float64, float64, string, error) {
	p, err := m.bestPair(ctx, chainID, token)
	if err != nil {
		return 0, 0, "", err
	}
	return p.Liquidity.USD, p.Volume.H24, p.PairAddress, nil
}

// Symbol 尽力返回代币符号（用于告警可读性）。
func (m *Market) Symbol(ctx context.Context, chainID, token string) string {
	p, err := m.bestPair(ctx, chainID, token)
	if err != nil {
		return ""
	}
	if p.BaseToken.Address != "" && strings.EqualFold(p.BaseToken.Address, token) {
		return p.BaseToken.Symbol
	}
	return p.QuoteToken.Symbol
}

// NewPairs 查询某链最近的高流动性交易对（用于发现新池，作为 pump.fun 等源的兜底）。
//
// DexScreener 的 search 端点支持按关键词检索，这里用链+关键词组合近似“最新热门”。
func (m *Market) NewPairs(ctx context.Context, chainID string, keyword string) ([]dexPair, error) {
	query := keyword
	if strings.TrimSpace(query) == "" {
		query = chainIDForAPI(chainID) + " meme"
	}
	var out dexResponse
	endpoint := fmt.Sprintf("%s/search?q=%s", m.baseURL, strings.ReplaceAll(query, " ", "%20"))
	if err := httpGetJSON(ctx, m.http, endpoint, nil, &out); err != nil {
		return nil, err
	}
	wantChain := chainIDForAPI(chainID)
	res := make([]dexPair, 0, len(out.Pairs))
	for _, p := range out.Pairs {
		if wantChain != "" && !strings.EqualFold(p.ChainID, wantChain) {
			continue
		}
		res = append(res, p)
	}
	return res, nil
}
