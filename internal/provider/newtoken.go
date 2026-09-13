package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// NewToken 表示一个新发现的可监控代币。
type NewToken struct {
	Chain     string    `json:"chain"`
	Address   string    `json:"address"`
	Symbol    string    `json:"symbol"`
	Name      string    `json:"name"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	// 附加指标（可空）
	LiquidityUSD float64 `json:"liquidity_usd,omitempty"`
	Volume24hUSD float64 `json:"volume_24h,omitempty"`
	PoolAddress  string  `json:"pool_address,omitempty"`
}

// NewTokenFeed 新币与热门池发现。
//
// 设计：多源可插拔，任意一源失败不影响其它源。
//   - pump.fun（Solana 新币发射，事实标准）
//   - DexScreener（多链兜底：按关键词检索近期热门池）
type NewTokenFeed struct {
	http    *http.Client
	cache   *cache
	pumpURL string
	market  *Market
}

// NewNewTokenFeed 构建新币发现服务。
func NewNewTokenFeed(market *Market) *NewTokenFeed {
	return &NewTokenFeed{
		http:    &http.Client{Timeout: 12 * time.Second},
		cache:   newCache(60 * time.Second),
		pumpURL: "https://frontend-api-v3.pump.fun",
		market:  market,
	}
}

// Latest 拉取指定链的最新代币。
//
// 注意：pump.fun 的公开前端接口未承诺稳定性，生产环境建议叠加 Moralis/Solana Tracker。
func (f *NewTokenFeed) Latest(ctx context.Context, chainID string, limit int) ([]NewToken, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	key := fmt.Sprintf("new:%s:%d", chainID, limit)
	if v, ok := f.cache.get(key); ok {
		if list, ok := v.([]NewToken); ok {
			return list, nil
		}
	}

	out := make([]NewToken, 0, limit)
	if strings.EqualFold(chainID, "solana") {
		tokens, err := f.pumpLatest(ctx, limit)
		if err == nil {
			out = append(out, tokens...)
		}
	}

	// DexScreener 兜底/补充
	if f.market != nil {
		pairs, err := f.market.NewPairs(ctx, chainID, "")
		if err == nil {
			for _, p := range pairs {
				if len(out) >= limit {
					break
				}
				out = append(out, NewToken{
					Chain:        chainID,
					Address:      p.BaseToken.Address,
					Symbol:       p.BaseToken.Symbol,
					Source:       "dexscreener",
					LiquidityUSD: p.Liquidity.USD,
					Volume24hUSD: p.Volume.H24,
					PoolAddress:  p.PairAddress,
				})
			}
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("newtoken: no source returned data for %s", chainID)
	}
	f.cache.put(key, out)
	return out, nil
}

// pumpLatest 调用 pump.fun 前端接口并宽松解析（字段可能变化）。
func (f *NewTokenFeed) pumpLatest(ctx context.Context, limit int) ([]NewToken, error) {
	endpoint := fmt.Sprintf("%s/coins/latest?limit=%d", f.pumpURL, limit)
	var raw []map[string]any
	if err := httpGetJSON(ctx, f.http, endpoint, nil, &raw); err != nil {
		return nil, err
	}

	out := make([]NewToken, 0, len(raw))
	for _, item := range raw {
		mint := firstNonEmpty(strValue(item, "mint"), strValue(item, "address"))
		if mint == "" {
			continue
		}
		t := NewToken{
			Chain:   "solana",
			Address: mint,
			Symbol:  strValue(item, "symbol"),
			Name:    strValue(item, "name"),
			Source:  "pumpfun",
		}
		if ts := floatValue(item, "created_timestamp"); ts > 0 {
			t.CreatedAt = time.Unix(int64(ts), 0).UTC()
		}
		out = append(out, t)
	}
	return out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
