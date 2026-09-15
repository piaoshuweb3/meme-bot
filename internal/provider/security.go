package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"meme-bot/internal/model"
)

// Security 实现链适配器的 SecuritySource：合约安全与持仓集中度。
//
// 数据源：
//  1. GoPlus（首选，需要可选 API Key；无 Key 时用公共端点，可能限流）
//  2. 本地启发式兜底：通过 DexScreener 判断是否存在有效池子（“有流动性”不等于“安全”，
//     因此兜底结果一律标记 Risky=true，交由策略层按配置决定是否放行）
type Security struct {
	http       *http.Client
	cache      *cache
	goplusKey  string
	goplusBase string
	market     *Market
}

// NewSecurity 构建安全服务（默认 GoPlus 公共端点）。
func NewSecurity(goplusAPIKey string, market *Market) *Security {
	return NewSecurityWithBaseURL(goplusAPIKey, market, "")
}

// NewSecurityWithBaseURL 覆盖 GoPlus API 基址（自建网关/代理/测试注入）。
// 传空串表示使用默认端点。
func NewSecurityWithBaseURL(goplusAPIKey string, market *Market, baseURL string) *Security {
	s := &Security{
		http:       &http.Client{Timeout: 12 * time.Second},
		cache:      newCache(5 * time.Minute),
		goplusKey:  strings.TrimSpace(goplusAPIKey),
		goplusBase: "https://api.gopluslabs.io/api/v1/token_security",
		market:     market,
	}
	if v := strings.TrimRight(strings.TrimSpace(baseURL), "/"); v != "" {
		s.goplusBase = v
	}
	return s
}

// Security 实现 SecuritySource。
func (s *Security) Security(ctx context.Context, chainID, token string) (*model.SecurityReport, error) {
	key := "sec:" + chainID + ":" + token
	if v, ok := s.cache.get(key); ok {
		if r, ok := v.(*model.SecurityReport); ok {
			return r, nil
		}
	}

	report, err := s.fromGoPlus(ctx, chainID, token)
	if err != nil {
		// 降级：给出保守（Risky=true）的兜底报告，而不是直接失败
		fallback, ferr := s.fallback(ctx, chainID, token)
		if ferr != nil {
			return nil, fmt.Errorf("security: goplus=%v; fallback=%v", err, ferr)
		}
		s.cache.put(key, fallback)
		return fallback, nil
	}
	s.cache.put(key, report)
	return report, nil
}

func (s *Security) fromGoPlus(ctx context.Context, chainID, token string) (*model.SecurityReport, error) {
	headers := map[string]string{}
	if s.goplusKey != "" {
		headers["Authorization"] = s.goplusKey
	}
	endpoint := fmt.Sprintf("%s/%s?contract_addresses=%s", s.goplusBase, goplusChainID(chainID), token)

	var raw struct {
		Code    int                       `json:"code"`
		Message string                    `json:"message"`
		Result  map[string]map[string]any `json:"result"`
	}
	if err := httpGetJSON(ctx, s.http, endpoint, headers, &raw); err != nil {
		return nil, err
	}
	if raw.Code != 1 {
		return nil, fmt.Errorf("goplus: code=%d message=%s", raw.Code, raw.Message)
	}

	item, ok := lookupCaseInsensitive(raw.Result, token)
	if !ok {
		return nil, fmt.Errorf("goplus: no result for %s", token)
	}

	rep := &model.SecurityReport{
		Chain:     chainID,
		Token:     token,
		Source:    "goplus",
		CheckedAt: time.Now().UTC(),
	}
	rep.IsHoneypot = flagValue(item, "is_honeypot")
	rep.HasMint = flagValue(item, "is_mintable") || flagValue(item, "is_mintable_token")
	rep.HasBlacklist = flagValue(item, "is_blacklisted")
	rep.IsOpenSource = flagValue(item, "is_open_source")
	rep.OwnershipRenounced = strings.TrimSpace(strValue(item, "owner_address")) == ""
	rep.Owner = strValue(item, "owner_address")
	rep.BuyTax = floatValue(item, "buy_tax")
	rep.SellTax = floatValue(item, "sell_tax")

	rep.Risky = rep.IsHoneypot || rep.HasMint || rep.HasBlacklist ||
		!rep.IsOpenSource || rep.BuyTax > 0.10 || rep.SellTax > 0.10 || !rep.OwnershipRenounced
	return rep, nil
}

// fallback 在没有安全数据源时给出保守结论（禁止自动放行）。
func (s *Security) fallback(ctx context.Context, chainID, token string) (*model.SecurityReport, error) {
	rep := &model.SecurityReport{
		Chain:     chainID,
		Token:     token,
		Source:    "fallback",
		CheckedAt: time.Now().UTC(),
		Risky:     true,
	}
	if s.market != nil {
		if _, _, pool, err := s.market.LiquidityUSD(ctx, chainID, token); err == nil && pool != "" {
			// 至少确认存在可交易池子
			rep.IsOpenSource = false
		}
	}
	return rep, nil
}

// Concentration 实现 SecuritySource：持仓集中度。
func (s *Security) Concentration(ctx context.Context, chainID, token string, topN int) (*model.Concentration, error) {
	key := "conc:" + chainID + ":" + token
	if v, ok := s.cache.get(key); ok {
		if c, ok := v.(*model.Concentration); ok {
			return c, nil
		}
	}

	headers := map[string]string{}
	if s.goplusKey != "" {
		headers["Authorization"] = s.goplusKey
	}
	endpoint := fmt.Sprintf("%s/%s?contract_addresses=%s", s.goplusBase, goplusChainID(chainID), token)

	var raw struct {
		Code   int                       `json:"code"`
		Result map[string]map[string]any `json:"result"`
	}
	if err := httpGetJSON(ctx, s.http, endpoint, headers, &raw); err != nil {
		return nil, err
	}
	item, ok := lookupCaseInsensitive(raw.Result, token)
	if !ok {
		return nil, fmt.Errorf("goplus: no holder data for %s", token)
	}

	c := &model.Concentration{Chain: chainID, Token: token}
	// EVM：top_10_holder_rate 为比例（0-1）
	c.Top10Percent = floatValue(item, "top_10_holder_rate")
	c.Top20Percent = floatValue(item, "top_20_holder_rate")
	if n, err := strconv.Atoi(strValue(item, "holder_count")); err == nil {
		c.HolderCount = n
	}
	s.cache.put(key, c)
	return c, nil
}

// ---- 宽松解析工具（各数据源字段类型不一致：字符串/数字/布尔混合）----

func lookupCaseInsensitive(m map[string]map[string]any, token string) (map[string]any, bool) {
	if v, ok := m[token]; ok {
		return v, true
	}
	for k, v := range m {
		if strings.EqualFold(k, token) {
			return v, true
		}
	}
	// 数据源偶尔返回单个结果但键名不同：取唯一一项
	if len(m) == 1 {
		for _, v := range m {
			return v, true
		}
	}
	return nil, false
}

func flagValue(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "1" || strings.EqualFold(t, "true")
	case float64:
		return t == 1
	default:
		return false
	}
}

func strValue(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", t)
	}
}

func floatValue(m map[string]any, key string) float64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0
		}
		return f
	case bool:
		if t {
			return 1
		}
		return 0
	default:
		return 0
	}
}
