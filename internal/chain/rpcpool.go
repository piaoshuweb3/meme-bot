package chain

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

// RPCPool 管理同一条链的多个 JSON-RPC 节点：健康检查 + 故障自动切换。
//
// 为什么需要它：单一 RPC 节点故障会导致漏事件、漏交易，是生产事故的常见来源。
type RPCPool struct {
	mu      sync.RWMutex
	name    string
	urls    []string
	healthy []bool
	idx     int
	client  *http.Client
}

// NewRPCPool 构建 RPC 池。
func NewRPCPool(name string, urls []string) *RPCPool {
	clean := make([]string, 0, len(urls))
	for _, u := range urls {
		if t := strings.TrimSpace(u); t != "" {
			clean = append(clean, t)
		}
	}
	p := &RPCPool{
		name:    name,
		urls:    clean,
		healthy: make([]bool, len(clean)),
		client:  &http.Client{Timeout: 15 * time.Second},
	}
	for i := range p.healthy {
		p.healthy[i] = true
	}
	return p
}

// Len 节点数量。
func (p *RPCPool) Len() int { return len(p.urls) }

// Call 依次尝试节点（跳过不健康节点），成功后返回结果到 out。
func (p *RPCPool) Call(ctx context.Context, method string, params []any, out any) error {
	if len(p.urls) == 0 {
		return fmt.Errorf("%s: no rpc url configured", p.name)
	}
	p.mu.RLock()
	start := p.idx
	p.mu.RUnlock()

	var lastErr error
	tried := 0
	for i := 0; i < len(p.urls); i++ {
		pos := (start + i) % len(p.urls)
		if !p.isHealthy(pos) {
			continue
		}
		tried++
		err := p.callAt(ctx, pos, method, params, out)
		if err == nil {
			return nil
		}
		lastErr = err
		p.markUnhealthy(pos)
	}
	if tried == 0 {
		// 全部节点之前被标记为不健康：强制探测一轮，避免永久熔断
		for pos := range p.urls {
			err := p.callAt(ctx, pos, method, params, out)
			if err == nil {
				p.markHealthy(pos)
				return nil
			}
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%s: all rpc nodes unhealthy", p.name)
	}
	return lastErr
}

func (p *RPCPool) callAt(ctx context.Context, pos int, method string, params []any, out any) error {
	if params == nil {
		params = []any{}
	}
	payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.urls[pos], strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode rpc response: %w", err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("rpc error %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("decode rpc result: %w", err)
	}
	return nil
}

func (p *RPCPool) isHealthy(pos int) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.healthy[pos]
}

func (p *RPCPool) markUnhealthy(pos int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.healthy[pos] = false
	p.idx = (pos + 1) % len(p.urls)
}

func (p *RPCPool) markHealthy(pos int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.healthy[pos] = true
}

// HealthCheck 主动探测所有节点，返回可用数量。
func (p *RPCPool) HealthCheck(ctx context.Context, probeMethod string) int {
	ok := 0
	for i := range p.urls {
		var result json.RawMessage
		if err := p.callAt(ctx, i, probeMethod, []any{}, &result); err == nil {
			p.markHealthy(i)
			ok++
			continue
		}
		p.markUnhealthy(i)
	}
	return ok
}

// URLs 返回配置的节点地址列表副本。
func (p *RPCPool) URLs() []string {
	out := make([]string, len(p.urls))
	copy(out, p.urls)
	return out
}
