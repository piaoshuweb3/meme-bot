package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// SmartMoney 封装“聪明钱 / 标签 / 趋势”类数据源。
//
// 重要说明（避免误用）：
//   - GMGN 没有公开、稳定、官方授权的 API，直接抓取存在合规与稳定性风险；
//     因此这里只保留**接口占位**，并在未配置代理端点时明确返回错误。
//   - 生产建议：使用 Birdeye / Bitquery / Nansen / Arkham 的官方 API 作为主力，
//     自建标签库（address_profiles + address_blacklist）作为长期资产。
type SmartMoney struct {
	http      *http.Client
	cache     *cache
	proxyBase string // 第三方代理/自建网关地址（可选）
	apiKey    string
}

// TrendToken 趋势榜代币。
type TrendToken struct {
	Chain        string  `json:"chain"`
	Address      string  `json:"address"`
	Symbol       string  `json:"symbol"`
	PriceUSD     float64 `json:"price_usd"`
	Volume24hUSD float64 `json:"volume_24h"`
	HolderCount  int     `json:"holder_count"`
	SmartMoneyIn int     `json:"smart_money_in"`
}

// NewSmartMoney 构建聪明钱数据源。proxyBase 为空表示未接入。
func NewSmartMoney(proxyBase, apiKey string) *SmartMoney {
	return &SmartMoney{
		http:      &http.Client{Timeout: 12 * time.Second},
		cache:     newCache(60 * time.Second),
		proxyBase: strings.TrimRight(strings.TrimSpace(proxyBase), "/"),
		apiKey:    strings.TrimSpace(apiKey),
	}
}

// Available 是否已配置可用通道。
func (s *SmartMoney) Available() bool { return s.proxyBase != "" }

// Trending 获取趋势榜。
func (s *SmartMoney) Trending(ctx context.Context, chainID, timeframe string) ([]TrendToken, error) {
	if !s.Available() {
		return nil, fmt.Errorf("smartmoney: 未配置数据源（GMGN 无公开稳定 API，请配置 proxyBase 或改用 Birdeye/Bitquery）")
	}
	if timeframe == "" {
		timeframe = "1h"
	}
	key := "trend:" + chainID + ":" + timeframe
	if v, ok := s.cache.get(key); ok {
		if list, ok := v.([]TrendToken); ok {
			return list, nil
		}
	}

	headers := map[string]string{}
	if s.apiKey != "" {
		headers["Authorization"] = "Bearer " + s.apiKey
	}
	endpoint := fmt.Sprintf("%s/trending/%s?timeframe=%s", s.proxyBase, chainIDForAPI(chainID), timeframe)

	var raw struct {
		Data []map[string]any `json:"data"`
	}
	if err := httpGetJSON(ctx, s.http, endpoint, headers, &raw); err != nil {
		return nil, err
	}

	out := make([]TrendToken, 0, len(raw.Data))
	for _, item := range raw.Data {
		addr := firstNonEmpty(strValue(item, "address"), strValue(item, "token_address"))
		if addr == "" {
			continue
		}
		out = append(out, TrendToken{
			Chain:        chainID,
			Address:      addr,
			Symbol:       strValue(item, "symbol"),
			PriceUSD:     floatValue(item, "price"),
			Volume24hUSD: floatValue(item, "volume_24h"),
			HolderCount:  int(floatValue(item, "holder_count")),
			SmartMoneyIn: int(floatValue(item, "smart_money_count")),
		})
	}
	s.cache.put(key, out)
	return out, nil
}

// AddressTags 查询地址标签（项目方 / CEX / 混币器 / 机器人）。
//
// 返回的标签会进入地址筛选漏斗的第一层（基础标签与黑名单）。
func (s *SmartMoney) AddressTags(ctx context.Context, chainID, address string) ([]string, error) {
	if !s.Available() {
		return nil, fmt.Errorf("smartmoney: 未配置数据源，无法查询地址标签")
	}
	headers := map[string]string{}
	if s.apiKey != "" {
		headers["Authorization"] = "Bearer " + s.apiKey
	}
	endpoint := fmt.Sprintf("%s/tags/%s/%s", s.proxyBase, chainIDForAPI(chainID), address)

	var raw struct {
		Tags []string `json:"tags"`
	}
	if err := httpGetJSON(ctx, s.http, endpoint, headers, &raw); err != nil {
		return nil, err
	}
	return raw.Tags, nil
}
