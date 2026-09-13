package aggregator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Jupiter 是 Solana 上的 Jupiter 聚合器客户端。
//
// Solana 与 EVM 的差异：/quote 返回报价 JSON，/swap 返回**已组装但未签名**的交易（base64）。
type Jupiter struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewJupiter 构建 Jupiter 客户端。
func NewJupiter(baseURL, apiKey string) *Jupiter {
	if baseURL == "" {
		baseURL = "https://quote-api.jup.ag/v6"
	}
	return &Jupiter{
		baseURL: trimRightSlash(baseURL),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

// Name 返回聚合器名称。
func (j *Jupiter) Name() string { return "jupiter" }

// QuoteRaw 获取原始报价 JSON（后续 /swap 需要原样回传）。
func (j *Jupiter) QuoteRaw(ctx context.Context, inputMint, outputMint, amount string, slippageBps int) (json.RawMessage, string, error) {
	q := url.Values{}
	q.Set("inputMint", inputMint)
	q.Set("outputMint", outputMint)
	q.Set("amount", amount)
	q.Set("slippageBps", fmt.Sprintf("%d", slippageBps))

	resp, body, err := j.do(ctx, http.MethodGet, fmt.Sprintf("%s/quote?%s", j.baseURL, q.Encode()), nil)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("jupiter quote http %d: %s", resp.StatusCode, clip(string(body), 200))
	}
	var out struct {
		OutAmount string `json:"outAmount"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, "", fmt.Errorf("jupiter quote decode: %w", err)
	}
	return body, out.OutAmount, nil
}

// SwapTxRequest 是 Jupiter /swap 请求体。
type SwapTxRequest struct {
	QuoteResponse             json.RawMessage `json:"quoteResponse"`
	UserPublicKey             string          `json:"userPublicKey"`
	WrapAndUnwrapSOL          bool            `json:"wrapAndUnwrapSOL"`
	UseSharedAccounts         bool            `json:"useSharedAccounts"`
	DynamicComputeUnitLimit   bool            `json:"dynamicComputeUnitLimit"`
	PrioritizationFeeLamports int             `json:"prioritizationFeeLamports,omitempty"`
}

// BuildTx 构建未签名交易（base64 字符串，签名后交给 privatetx 的 Jito 通道广播）。
func (j *Jupiter) BuildTx(ctx context.Context, quoteRaw json.RawMessage, userPubKey string, priorityFeeLamports int) (string, error) {
	reqBody := SwapTxRequest{
		QuoteResponse:             quoteRaw,
		UserPublicKey:             userPubKey,
		WrapAndUnwrapSOL:          true,
		UseSharedAccounts:         true,
		DynamicComputeUnitLimit:   true,
		PrioritizationFeeLamports: priorityFeeLamports,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	resp, body, err := j.do(ctx, http.MethodPost, j.baseURL+"/swap", payload)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("jupiter swap http %d: %s", resp.StatusCode, clip(string(body), 200))
	}
	var out struct {
		SwapTransaction string `json:"swapTransaction"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("jupiter swap decode: %w", err)
	}
	if out.SwapTransaction == "" {
		return "", fmt.Errorf("jupiter swap: empty transaction")
	}
	return out.SwapTransaction, nil
}

func (j *Jupiter) do(ctx context.Context, method, endpoint string, body []byte) (*http.Response, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if j.apiKey != "" {
		req.Header.Set("x-api-key", j.apiKey)
	}
	resp, err := j.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return resp, raw, nil
}

func trimRightSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
