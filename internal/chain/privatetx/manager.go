// Package privatetx 统一封装私有交易通道，规避公开 mempool 的 MEV 风险。
//
//   - EVM：Flashbots Protect / MEV Blocker 等“私有 RPC”（JSON-RPC eth_sendRawTransaction）
//   - Solana：Jito Bundle（/api/v1/bundles）
//
// 执行层必须优先走本包；全部通道不可用时才允许降级到公开通道，并触发 Critical 告警。
package privatetx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Options 私有交易选项。
type Options struct {
	MaxPriorityFee *big.Int
	BundleOnly     bool
	// RefundAddress：支持 MEV 返还的通道（如 MEV Blocker）可指定返还地址
	RefundAddress string
	// Encoding：载荷编码，"hex"（EVM，默认）或 "base64"（Solana）
	Encoding string
}

// Sender 私有交易发送器。
type Sender interface {
	Name() string
	SupportsChain(chainID string) bool
	SendPrivate(ctx context.Context, chainID, signedPayload string, opts Options) (string, error)
}

// ---------------------------------------------------------------------------
// Manager
// ---------------------------------------------------------------------------

// Manager 按链维护私有通道（按注册顺序优先尝试）。
type Manager struct {
	mu      sync.RWMutex
	senders map[string][]Sender
}

// NewManager 构建私有交易管理器。
func NewManager() *Manager {
	return &Manager{senders: make(map[string][]Sender)}
}

// Register 注册某条链的私有通道。
func (m *Manager) Register(chainID string, s Sender) {
	if s == nil || strings.TrimSpace(chainID) == "" {
		return
	}
	key := strings.ToLower(strings.TrimSpace(chainID))
	m.mu.Lock()
	defer m.mu.Unlock()
	m.senders[key] = append(m.senders[key], s)
}

// Channels 返回某条链已注册的通道名。
func (m *Manager) Channels(chainID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := m.senders[strings.ToLower(strings.TrimSpace(chainID))]
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.Name())
	}
	return out
}

// Available 是否存在可用的私有通道。
func (m *Manager) Available(chainID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.senders[strings.ToLower(strings.TrimSpace(chainID))]) > 0
}

// ErrNoPrivateChannel 目标链没有可用私有通道。
var ErrNoPrivateChannel = fmt.Errorf("privatetx: no private channel available")

// Send 依次尝试私有通道，成功返回 (txHash, 通道名, nil)。
func (m *Manager) Send(ctx context.Context, chainID, signedPayload string, opts Options) (string, string, error) {
	m.mu.RLock()
	list := append([]Sender(nil), m.senders[strings.ToLower(strings.TrimSpace(chainID))]...)
	m.mu.RUnlock()

	if len(list) == 0 {
		return "", "", ErrNoPrivateChannel
	}
	var errs []string
	for _, s := range list {
		hash, err := s.SendPrivate(ctx, chainID, signedPayload, opts)
		if err == nil && hash != "" {
			return hash, s.Name(), nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", s.Name(), err))
	}
	return "", "", fmt.Errorf("privatetx: all channels failed: %s", strings.Join(errs, "; "))
}

// ---------------------------------------------------------------------------
// EVM：私有 RPC 风格（Flashbots Protect / MEV Blocker）
// ---------------------------------------------------------------------------

// RPCProtectSender 通过私有 RPC 端点提交原始交易（不进公开 mempool）。
type RPCProtectSender struct {
	name   string
	rpcURL string
	chains []string
	http   *http.Client
}

// NewRPCProtectSender 构建私有 RPC 发送器；chains 为空表示不限制链。
func NewRPCProtectSender(name, rpcURL string, chains ...string) *RPCProtectSender {
	if strings.TrimSpace(name) == "" {
		name = "private-rpc"
	}
	normalized := make([]string, 0, len(chains))
	for _, c := range chains {
		if t := strings.ToLower(strings.TrimSpace(c)); t != "" {
			normalized = append(normalized, t)
		}
	}
	return &RPCProtectSender{
		name:   name,
		rpcURL: rpcURL,
		chains: normalized,
		http:   &http.Client{Timeout: 20 * time.Second},
	}
}

// Name 实现 Sender。
func (s *RPCProtectSender) Name() string { return s.name }

// SupportsChain 实现 Sender。
func (s *RPCProtectSender) SupportsChain(chainID string) bool {
	if len(s.chains) == 0 {
		return true
	}
	chainID = strings.ToLower(strings.TrimSpace(chainID))
	for _, c := range s.chains {
		if c == chainID {
			return true
		}
	}
	return false
}

// SendPrivate 实现 Sender：eth_sendRawTransaction 到私有 RPC。
func (s *RPCProtectSender) SendPrivate(ctx context.Context, chainID, signedPayload string, opts Options) (string, error) {
	if !s.SupportsChain(chainID) {
		return "", fmt.Errorf("%s: unsupported chain %s", s.name, chainID)
	}
	if strings.TrimSpace(s.rpcURL) == "" {
		return "", fmt.Errorf("%s: empty rpc url", s.name)
	}
	payload := signedPayload
	if !strings.HasPrefix(payload, "0x") {
		payload = "0x" + payload
	}

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "eth_sendRawTransaction",
		"params":  []any{payload},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.rpcURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var out struct {
		Result string `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("%s: decode response: %w", s.name, err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("%s: rpc error %d: %s", s.name, out.Error.Code, out.Error.Message)
	}
	if out.Result == "" {
		return "", fmt.Errorf("%s: empty tx hash", s.name)
	}
	return out.Result, nil
}

// ---------------------------------------------------------------------------
// Solana：Jito Bundle
// ---------------------------------------------------------------------------

// JitoSender 通过 Jito Block Engine 提交 bundle（Solana 私有通道）。
type JitoSender struct {
	blockEngineURL string
	http           *http.Client
}

// NewJitoSender 构建 Jito 发送器；blockEngineURL 形如 https://mainnet.block-engine.jito.wtf。
func NewJitoSender(blockEngineURL string) *JitoSender {
	return &JitoSender{
		blockEngineURL: strings.TrimRight(strings.TrimSpace(blockEngineURL), "/"),
		http:           &http.Client{Timeout: 25 * time.Second},
	}
}

// Name 实现 Sender。
func (s *JitoSender) Name() string { return "jito" }

// SupportsChain 实现 Sender：仅 Solana。
func (s *JitoSender) SupportsChain(chainID string) bool {
	return strings.EqualFold(chainID, "solana")
}

// SendPrivate 实现 Sender：POST /api/v1/bundles，载荷为 base64 已签名交易。
func (s *JitoSender) SendPrivate(ctx context.Context, chainID, signedPayload string, opts Options) (string, error) {
	if !s.SupportsChain(chainID) {
		return "", fmt.Errorf("jito: unsupported chain %s", chainID)
	}
	if s.blockEngineURL == "" {
		return "", fmt.Errorf("jito: empty block engine url")
	}
	if opts.Encoding != "base64" {
		return "", fmt.Errorf("jito: payload encoding must be base64, got %q", opts.Encoding)
	}

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "sendBundle",
		"params":  []any{[]string{signedPayload}},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.blockEngineURL+"/api/v1/bundles", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var out struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("jito: decode response: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("jito: rpc error: %s", out.Error.Message)
	}
	if out.Result == "" {
		return "", fmt.Errorf("jito: empty bundle id")
	}
	return out.Result, nil
}

var _ Sender = (*RPCProtectSender)(nil)
var _ Sender = (*JitoSender)(nil)
