package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func dexServer(t *testing.T, body string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// dexPairsBody 三个池子：base 链两个（流动性 1 万 / 9 万），solana 链一个（流动性最高但链不符）。
const dexPairsBody = `{"pairs":[
 {"chainId":"base","dexId":"uniswap","pairAddress":"0xpool-lo","priceUsd":"0.5","volume":{"h24":1000},"liquidity":{"usd":10000},"baseToken":{"address":"0xT","symbol":"MEME"},"quoteToken":{"address":"0xusdc","symbol":"USDC"}},
 {"chainId":"base","dexId":"aerodrome","pairAddress":"0xpool-hi","priceUsd":"0.51","volume":{"h24":50000},"liquidity":{"usd":90000},"baseToken":{"address":"0xT","symbol":"MEME"},"quoteToken":{"address":"0xweth","symbol":"WETH"}},
 {"chainId":"solana","dexId":"raydium","pairAddress":"0xpool-sol","priceUsd":"9.9","volume":{"h24":1},"liquidity":{"usd":999999},"baseToken":{"address":"0xT","symbol":"MEME"},"quoteToken":{"address":"0xsol","symbol":"SOL"}}
]}`

func TestMarketPicksHighestLiquidityPairWithinChain(t *testing.T) {
	srv, calls := dexServer(t, dexPairsBody)
	m := NewMarketWithBaseURL(srv.URL)

	price, source, err := m.PriceUSD(context.Background(), "base", "0xT")
	if err != nil {
		t.Fatalf("PriceUSD 失败：%v", err)
	}
	if price != 0.51 {
		t.Fatalf("应选 base 链上流动性最高的池子（0.51），实际 %v", price)
	}
	if !strings.HasPrefix(source, "dexscreener:aerodrome") {
		t.Fatalf("来源应含命中的 dexId：%q", source)
	}

	liq, vol, pool, err := m.LiquidityUSD(context.Background(), "base", "0xT")
	if err != nil {
		t.Fatalf("LiquidityUSD 失败：%v", err)
	}
	if liq != 90000 || vol != 50000 || pool != "0xpool-hi" {
		t.Fatalf("应与价格取自同一池子：liq=%v vol=%v pool=%q", liq, vol, pool)
	}

	if got := m.Symbol(context.Background(), "base", "0xT"); got != "MEME" {
		t.Fatalf("应取 baseToken 符号：%q", got)
	}

	// 缓存：同一 key 的连续查询只应产生一次上游请求
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Fatalf("应命中 TTL 缓存（仅 1 次上游请求），实际 %d", n)
	}
}

func TestMarketFallsBackToHighestLiquidityWhenChainMissing(t *testing.T) {
	srv, _ := dexServer(t, dexPairsBody)
	m := NewMarketWithBaseURL(srv.URL)

	// 查询 arbitrum：无匹配链记录 → 退化为整体流动性最高的池子（solana 那个）
	price, _, err := m.PriceUSD(context.Background(), "arbitrum", "0xT")
	if err != nil {
		t.Fatalf("无匹配链时应退化而非报错：%v", err)
	}
	if price != 9.9 {
		t.Fatalf("应退化为整体流动性最高的池子：%v", price)
	}
}

func TestMarketReportsNoPairWhenEmpty(t *testing.T) {
	srv, _ := dexServer(t, `{"pairs":[]}`)
	m := NewMarketWithBaseURL(srv.URL)

	_, _, err := m.PriceUSD(context.Background(), "base", "0xT")
	if err == nil || !strings.Contains(err.Error(), "no pair") {
		t.Fatalf("空结果应报 no pair：%v", err)
	}
	if got := m.Symbol(context.Background(), "base", "0xT"); got != "" {
		t.Fatalf("Symbol 失败时应返回空串（不阻断告警）：%q", got)
	}
}

func TestMarketRejectsUnparsablePrice(t *testing.T) {
	srv, _ := dexServer(t, `{"pairs":[{"chainId":"base","dexId":"x","pairAddress":"0xp","priceUsd":"abc","liquidity":{"usd":1}}]}`)
	m := NewMarketWithBaseURL(srv.URL)

	_, _, err := m.PriceUSD(context.Background(), "base", "0xT")
	if err == nil || !strings.Contains(err.Error(), "invalid price") {
		t.Fatalf("非法价格应显式报错（避免按 0 价下单）：%v", err)
	}
}

func TestMarketNewPairsFiltersByChain(t *testing.T) {
	srv, _ := dexServer(t, dexPairsBody)
	m := NewMarketWithBaseURL(srv.URL)

	pairs, err := m.NewPairs(context.Background(), "base", "meme")
	if err != nil {
		t.Fatalf("NewPairs 失败：%v", err)
	}
	if len(pairs) != 2 {
		t.Fatalf("应仅返回 base 链的 2 个池子，实际 %d", len(pairs))
	}
	for _, p := range pairs {
		if !strings.EqualFold(p.ChainID, "base") {
			t.Fatalf("混入其它链的池子：%+v", p)
		}
	}
}

func TestProviderConstructorsHonorEmptyBaseURL(t *testing.T) {
	// 空串表示使用默认端点（不覆盖）
	if m := NewMarketWithBaseURL("  "); m.baseURL != "https://api.dexscreener.com/latest/dex" {
		t.Fatalf("空基址应保留默认端点：%q", m.baseURL)
	}
	// 尾斜杠应被规整
	if m := NewMarketWithBaseURL("http://gw.local/api/"); m.baseURL != "http://gw.local/api" {
		t.Fatalf("应去除尾斜杠：%q", m.baseURL)
	}
	if s := NewSecurityWithBaseURL("k", nil, "http://gw.local/"); s.goplusBase != "http://gw.local" {
		t.Fatalf("安全服务基址规整失败：%q", s.goplusBase)
	}
	if s := NewSecurity("k", nil); s.goplusBase != "https://api.gopluslabs.io/api/v1/token_security" {
		t.Fatalf("默认 GoPlus 端点错误：%q", s.goplusBase)
	}
}
