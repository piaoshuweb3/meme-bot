// Package provider 集成外部数据源，统一为策略层需要的三类信息：
//
//	行情（价格/流动性）· 安全（合约风险）· 新币/聪明钱（机会发现）
//
// 数据源优先级在 configs/chains.yaml 的 providers 段声明；
// 每个数据源都做缓存与降级，单一源故障不会中断监控。
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// cacheEntry 是带 TTL 的缓存项。
type cacheEntry struct {
	value   any
	expires time.Time
}

// cache 是极简 TTL 缓存（避免为小需求引入依赖）。
type cache struct {
	mu   sync.Mutex
	data map[string]cacheEntry
	ttl  time.Duration
}

func newCache(ttl time.Duration) *cache {
	return &cache{data: make(map[string]cacheEntry), ttl: ttl}
}

func (c *cache) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.value, true
}

func (c *cache) put(key string, v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = cacheEntry{value: v, expires: time.Now().Add(c.ttl)}
}

// httpGetJSON 发起 GET 请求并解析 JSON。
func httpGetJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d: %s", resp.StatusCode, clip(string(body), 160))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// chainIDForAPI 把内部链 ID 映射为各 API 需要的链标识。
func chainIDForAPI(chainID string) string {
	switch strings.ToLower(chainID) {
	case "base":
		return "base"
	case "solana":
		return "solana"
	case "bsc":
		return "bsc"
	case "ethereum", "eth":
		return "ethereum"
	case "arbitrum":
		return "arbitrum"
	default:
		return strings.ToLower(chainID)
	}
}

// goplusChainID 返回 GoPlus 使用的链 ID（EVM 用数字，Solana 用 "solana"）。
func goplusChainID(chainID string) string {
	switch strings.ToLower(chainID) {
	case "base":
		return "8453"
	case "bsc":
		return "56"
	case "ethereum", "eth":
		return "1"
	case "arbitrum":
		return "42161"
	case "solana":
		return "solana"
	default:
		return strings.ToLower(chainID)
	}
}
