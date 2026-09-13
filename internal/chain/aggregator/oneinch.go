// Package aggregator 封装 DEX 聚合器客户端（1inch / Jupiter 等）。
//
// 执行层只依赖统一的路由结构；聚合器不可用时由调用方降级到原生 Router。
package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Request 路由请求（跨链统一）。
type Request struct {
	ChainID     int64 // EVM 链 ID（Solana 忽略）
	TokenIn     string
	TokenOut    string
	AmountIn    string // 最小单位十进制字符串
	From        string // 发起/收款地址
	SlippageBps int
	Deadline    int64
}

// Route 路由结果（未签名交易）。
type Route struct {
	Router    string `json:"router"`
	To        string `json:"to"`
	Data      string `json:"data"`
	Value     string `json:"value"`
	Gas       uint64 `json:"gas"`
	GasPrice  string `json:"gas_price,omitempty"`
	AmountOut string `json:"amount_out,omitempty"`
}

// ---------------------------------------------------------------------------
// 1inch（EVM 通用）
// ---------------------------------------------------------------------------

// OneInch 是 1inch v6 Swap API 客户端。
//
// apiKey 从环境变量注入（config.Chain.AggregatorAPIKeyEnv），留空时走公共限流接口。
type OneInch struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewOneInch 构建 1inch 客户端。
func NewOneInch(baseURL, apiKey string) *OneInch {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.1inch.dev/swap/v6.1"
	}
	return &OneInch{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Name 返回聚合器名称。
func (c *OneInch) Name() string { return "1inch" }

// Quote 获取预估输出（用于滑点与成交质量校验）。
func (c *OneInch) Quote(ctx context.Context, req Request) (*Route, error) {
	q := url.Values{}
	q.Set("src", req.TokenIn)
	q.Set("dst", req.TokenOut)
	q.Set("amount", req.AmountIn)

	resp, body, err := c.get(ctx, fmt.Sprintf("%s/%d/quote?%s", c.baseURL, req.ChainID, q.Encode()))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("1inch quote http %d: %s", resp.StatusCode, clip(string(body), 200))
	}
	var out struct {
		DstAmount string `json:"dstAmount"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("1inch quote decode: %w", err)
	}
	return &Route{Router: c.Name(), AmountOut: out.DstAmount}, nil
}

// Build 构建可签名的 swap 交易 calldata。
func (c *OneInch) Build(ctx context.Context, req Request) (*Route, error) {
	q := url.Values{}
	q.Set("src", req.TokenIn)
	q.Set("dst", req.TokenOut)
	q.Set("amount", req.AmountIn)
	q.Set("from", req.From)
	q.Set("slippage", strconv.FormatFloat(float64(req.SlippageBps)/100.0, 'f', 2, 64))
	q.Set("disableEstimate", "true")
	q.Set("allowPartialFill", "false")
	if req.Deadline > 0 {
		q.Set("deadline", strconv.FormatInt(req.Deadline, 10))
	}

	resp, body, err := c.get(ctx, fmt.Sprintf("%s/%d/swap?%s", c.baseURL, req.ChainID, q.Encode()))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("1inch swap http %d: %s", resp.StatusCode, clip(string(body), 200))
	}
	var out struct {
		DstAmount string `json:"dstAmount"`
		Tx        struct {
			To       string `json:"to"`
			Data     string `json:"data"`
			Value    string `json:"value"`
			Gas      uint64 `json:"gas"`
			GasPrice string `json:"gasPrice"`
		} `json:"tx"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("1inch swap decode: %w", err)
	}
	return &Route{
		Router:    c.Name(),
		To:        out.Tx.To,
		Data:      out.Tx.Data,
		Value:     zeroIfEmpty(out.Tx.Value),
		Gas:       out.Tx.Gas,
		GasPrice:  out.Tx.GasPrice,
		AmountOut: out.DstAmount,
	}, nil
}

func (c *OneInch) get(ctx context.Context, endpoint string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return resp, body, nil
}

func zeroIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "0"
	}
	return s
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
