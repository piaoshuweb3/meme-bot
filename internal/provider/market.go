package provider

import (
	"context"
	"fmt"
	"math"
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

// NewMarket 构建行情服务（默认 DexScreener 公共端点）。
func NewMarket() *Market { return NewMarketWithBaseURL("") }

// NewMarketWithBaseURL 覆盖行情 API 基址。
//
// 用途：① 自建网关/代理（公共端点在国内常被限流）；② 单元测试注入 httptest 服务；
// ③ 回测时指向录制的行情回放服务。传空串表示使用默认端点。
func NewMarketWithBaseURL(baseURL string) *Market {
	m := &Market{
		http:    &http.Client{Timeout: 12 * time.Second},
		cache:   newCache(30 * time.Second),
		baseURL: "https://api.dexscreener.com/latest/dex",
	}
	if v := strings.TrimRight(strings.TrimSpace(baseURL), "/"); v != "" {
		m.baseURL = v
	}
	return m
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

// ---- 多源交叉校验：同一代币多池价格一致性 ----

// 一致性检查的默认参数。
const (
	// defaultMinLiquidityShare 参与比较的池子，其流动性至少为基准池的该比例。
	// 小池噪声极大，纳入比较会把正常代币误判为冲突 —— 这是本检查不误报的关键。
	defaultMinLiquidityShare = 0.10
	// defaultMaxPriceDeviation 允许的最大价格偏离（20%）。
	defaultMaxPriceDeviation = 0.20
)

// ClassifyPairConsistency 纯函数：由池子集合判定价格一致性。
//
// 语义：
//   - 只用"流动性足够大"的池做交叉校验，小池忽略；
//   - 可比池不足 2 个时返回一致（无法交叉校验，而非"有问题"）；
//   - 价格无法解析的池忽略（不猜价格）。
func ClassifyPairConsistency(pairs []dexPair, minLiquidityShare, maxDeviation float64) (bool, string) {
	if minLiquidityShare <= 0 {
		minLiquidityShare = defaultMinLiquidityShare
	}
	if maxDeviation <= 0 {
		maxDeviation = defaultMaxPriceDeviation
	}

	type cand struct {
		pool      string
		price     float64
		liquidity float64
	}
	valid := make([]cand, 0, len(pairs))
	for _, p := range pairs {
		if p.Liquidity.USD <= 0 {
			continue
		}
		var price float64
		if _, err := fmt.Sscanf(strings.TrimSpace(p.PriceUSD), "%f", &price); err != nil || price <= 0 {
			continue
		}
		valid = append(valid, cand{pool: p.PairAddress, price: price, liquidity: p.Liquidity.USD})
	}
	if len(valid) < 2 {
		return true, "可比池不足 2 个，无法交叉校验"
	}

	best := valid[0]
	for _, c := range valid[1:] {
		if c.liquidity > best.liquidity {
			best = c
		}
	}

	threshold := best.liquidity * minLiquidityShare
	compared, worstDev, worstPool := 0, 0.0, ""
	for _, c := range valid {
		if c.pool == best.pool || c.liquidity < threshold {
			continue
		}
		compared++
		if dev := math.Abs(c.price-best.price) / best.price; dev > worstDev {
			worstDev, worstPool = dev, c.pool
		}
	}
	if compared == 0 {
		return true, "除基准池外无可比池（其余池流动性过小），无法交叉校验"
	}
	if worstDev > maxDeviation {
		return false, fmt.Sprintf("池 %s 价格偏离 %.0f%%（基准池 %s 价 %.6g，阈值 %.0f%%）",
			shortenAddr(worstPool), worstDev*100, shortenAddr(best.pool), best.price, maxDeviation*100)
	}
	return true, fmt.Sprintf("可比池 %d 个，最大偏离 %.1f%%", compared, worstDev*100)
}

func shortenAddr(a string) string {
	if len(a) <= 12 {
		return a
	}
	return a[:6] + "..." + a[len(a)-4:]
}

// PairConsistency 检查该代币多池之间的价格一致性（带缓存）。
//
// 为什么需要：单一来源可能给出错误或被人为操纵的价格；同代币多池价格本应收敛，
// 偏离过大即说明数据可疑，应标记冲突交人工复核，而不是照单全收。
func (m *Market) PairConsistency(ctx context.Context, chainID, token string) (bool, string, error) {
	pairs, err := m.pairsFor(ctx, chainID, token)
	if err != nil {
		return true, "", err
	}
	consistent, detail := ClassifyPairConsistency(pairs, defaultMinLiquidityShare, defaultMaxPriceDeviation)
	return consistent, detail, nil
}

// pairsFor 拉取该代币的全部交易对（独立缓存键，避免与 bestPair 互相污染）。
func (m *Market) pairsFor(ctx context.Context, chainID, token string) ([]dexPair, error) {
	key := "pairs:" + chainID + ":" + token
	if v, ok := m.cache.get(key); ok {
		if p, ok := v.([]dexPair); ok {
			return p, nil
		}
	}
	var out dexResponse
	endpoint := fmt.Sprintf("%s/tokens/%s", m.baseURL, token)
	if err := httpGetJSON(ctx, m.http, endpoint, nil, &out); err != nil {
		return nil, err
	}
	m.cache.put(key, out.Pairs)
	return out.Pairs, nil
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
