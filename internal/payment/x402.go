// Package payment 提供 x402 微支付客户端：请求付费资源时自动完成链上稳定币支付并重试。
//
// x402 协议（HTTP 402 Payment Required）流程：
//  1. 客户端请求资源 → 服务端返回 402，并在响应体/头中给出支付要求（accepts 列表）；
//  2. 客户端选择自己支持的要求，构造支付凭证（EVM 上为 EIP-3009 授权签名）；
//  3. 客户端带 X-PAYMENT 头重试 → 服务端（或 facilitator）验证并结算 → 200。
//
// 安全阀：单次自动支付金额上限（MaxPayAmount）+ 仅支持白名单网络/资产。
package payment

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// headerPayment 是 x402 的支付凭证请求头。
const headerPayment = "X-PAYMENT"

// Requirement 是服务端给出的单条支付要求（x402 字段子集）。
type Requirement struct {
	Scheme            string         `json:"scheme"`            // 通常为 "exact"
	Network           string         `json:"network"`           // base / base-sepolia / polygon ...
	MaxAmountRequired string         `json:"maxAmountRequired"` // 最小单位（USDC 为 6 位小数）
	Resource          string         `json:"resource"`
	Description       string         `json:"description"`
	MimeType          string         `json:"mimeType"`
	PayTo             string         `json:"payTo"`
	MaxTimeoutSeconds int            `json:"maxTimeoutSeconds"`
	Asset             string         `json:"asset"` // ERC-20 合约地址
	Extra             map[string]any `json:"extra,omitempty"`
}

// PaymentPayload 是提交给服务端的凭证信封。
type PaymentPayload struct {
	X402Version int             `json:"x402Version"`
	Scheme      string          `json:"scheme"`
	Network     string          `json:"network"`
	Payload     json.RawMessage `json:"payload"`
}

// Payer 负责针对支付要求生成凭证。
type Payer interface {
	// Address 付款地址（日志与对账用）。
	Address() string
	// Supports 是否支持该网络与资产。
	Supports(network, asset string) bool
	// CreatePayload 生成凭证载荷（JSON）。
	CreatePayload(ctx context.Context, req Requirement) (json.RawMessage, error)
}

// Client x402 客户端。
type Client struct {
	payer Payer
	http  *http.Client
	// MaxPayAmount 单次自动支付上限（最小单位十进制字符串）；空表示不限制（不推荐）。
	MaxPayAmount string
}

// NewClient 构建客户端；timeout <= 0 时使用 30s。
func NewClient(payer Payer, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{payer: payer, http: &http.Client{Timeout: timeout}}
}

// ErrPaymentRequired 携带支付要求，表示"需要支付但被策略拒绝/不支持"。
type UnpayableError struct {
	Reason      string
	Requirement Requirement
}

// Error 实现 error。
func (e *UnpayableError) Error() string {
	return "x402: " + e.Reason
}

// Do 发送请求；遇到 402 时自动支付并重试一次。
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("x402: nil request")
	}
	if c.payer == nil {
		return nil, fmt.Errorf("x402: 未配置 payer")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusPaymentRequired {
		return resp, nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()

	requirements, err := ParseRequirements(body)
	if err != nil {
		return nil, err
	}

	chosen, ok := c.selectRequirement(requirements)
	if !ok {
		return nil, &UnpayableError{Reason: "没有可支付的选项（网络/资产不受支持）", Requirement: requirements[0]}
	}
	if err := c.checkLimit(chosen); err != nil {
		return nil, err
	}

	payload, err := c.payer.CreatePayload(ctx, chosen)
	if err != nil {
		return nil, fmt.Errorf("x402: 生成支付凭证失败: %w", err)
	}
	header, err := encodePaymentHeader(chosen, payload)
	if err != nil {
		return nil, err
	}

	retry := req.Clone(ctx)
	retry.Header.Set(headerPayment, header)
	return c.http.Do(retry)
}

// selectRequirement 选择第一个受支持的要求。
func (c *Client) selectRequirement(list []Requirement) (Requirement, bool) {
	for _, r := range list {
		if c.payer.Supports(r.Network, r.Asset) {
			return r, true
		}
	}
	return Requirement{}, false
}

// checkLimit 校验支付金额上限（防误付）。
func (c *Client) checkLimit(r Requirement) error {
	if strings.TrimSpace(c.MaxPayAmount) == "" {
		return nil
	}
	want, ok1 := parseAmount(r.MaxAmountRequired)
	max, ok2 := parseAmount(c.MaxPayAmount)
	if !ok1 || !ok2 {
		return &UnpayableError{Reason: "金额格式非法，拒绝支付", Requirement: r}
	}
	if want.Cmp(max) > 0 {
		return &UnpayableError{
			Reason:      fmt.Sprintf("所需金额 %s 超过单次上限 %s", r.MaxAmountRequired, c.MaxPayAmount),
			Requirement: r,
		}
	}
	return nil
}

// ParseRequirements 解析 402 响应体，兼容两种结构：
//
//	{"x402Version":1,"accepts":[{...},{...}]}   // 标准
//	{"scheme":"exact","payTo":"0x..",...}       // 单个要求
func ParseRequirements(body []byte) ([]Requirement, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("x402: 402 响应体为空")
	}

	var wrapper struct {
		Accepts []Requirement `json:"accepts"`
	}
	if err := json.Unmarshal(trimmed, &wrapper); err == nil && len(wrapper.Accepts) > 0 {
		return wrapper.Accepts, nil
	}

	var single Requirement
	if err := json.Unmarshal(trimmed, &single); err == nil && strings.TrimSpace(single.PayTo) != "" {
		return []Requirement{single}, nil
	}
	return nil, fmt.Errorf("x402: 无法解析支付要求（既无 accepts 数组，也无 payTo 字段）")
}

// encodePaymentHeader 生成 X-PAYMENT 头（base64(JSON)）。
func encodePaymentHeader(r Requirement, payload json.RawMessage) (string, error) {
	envelope := PaymentPayload{
		X402Version: 1,
		Scheme:      defaultScheme(r.Scheme),
		Network:     r.Network,
		Payload:     payload,
	}
	blob, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("x402: 编码凭证失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(blob), nil
}

// DecodePaymentHeader 解码 X-PAYMENT 头（服务端或测试使用）。
func DecodePaymentHeader(header string) (*PaymentPayload, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header))
	if err != nil {
		return nil, fmt.Errorf("x402: 凭证不是合法 base64: %w", err)
	}
	var envelope PaymentPayload
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("x402: 凭证不是合法 JSON: %w", err)
	}
	if envelope.X402Version == 0 {
		envelope.X402Version = 1
	}
	return &envelope, nil
}

func defaultScheme(s string) string {
	if strings.TrimSpace(s) == "" {
		return "exact"
	}
	return s
}
