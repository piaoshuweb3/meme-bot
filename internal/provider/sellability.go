// 实证可卖性（empirical sellability）：用**链上真实历史**判断"能不能卖出"，
// 而不是只看静态安全报告。
//
// 为什么必要：静态检测（蜜罐标志位、税率、权限）覆盖不了"能买不能卖"的实现——
// 不少蜜罐在权限与报税字段上都正常，只在 transfer/transferFrom 路径对特定地址 revert。
// 最可靠的判据是**实证**：真实发生过卖出，才证明这条路是通的。
//
// 判据（保守；样本不足不下结论，与影子跟踪的"样本不足禁止调参"同一纪律）：
//   - 存在卖出记录            → sellable
//   - 只有买入、无任何卖出且样本足够 → no_sell_path（强烈提示不可卖）
//   - 样本不足                → unknown（不确定就是不确定，不假装通过）
package provider

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"meme-bot/internal/chain"
)

// SellabilityVerdict 实证结论。
type SellabilityVerdict string

const (
	// VerdictUnknown 样本不足，不下结论（调用方应按保守策略处理，而非视为安全）。
	VerdictUnknown SellabilityVerdict = "unknown"
	// VerdictSellable 链上存在真实卖出，可卖路径已验证。
	VerdictSellable SellabilityVerdict = "sellable"
	// VerdictNoSellPath 有足够买入样本但零卖出，强烈提示不可卖。
	VerdictNoSellPath SellabilityVerdict = "no_sell_path"
)

// MinSellSamples 判定"无人卖出"所需的最小买入样本数。
const MinSellSamples = 5

// DefaultSellLookbackBlocks 默认回看区块数（覆盖近期活跃交易）。
const DefaultSellLookbackBlocks = 7200

// SellabilityReport 实证结果。
type SellabilityReport struct {
	Chain     string             `json:"chain"`
	Token     string             `json:"token"`
	Pool      string             `json:"pool"`
	Verdict   SellabilityVerdict `json:"verdict"`
	Buys      int                `json:"buys"`
	Sells     int                `json:"sells"`
	SellRatio float64            `json:"sell_ratio"`
	Evidence  string             `json:"evidence"`
	Source    string             `json:"source"`
	CheckedAt time.Time          `json:"checked_at"`
}

// Sellable 返回是否为"已实证可卖"。unknown 与 no_sell_path 都返回 false
// —— 证据不足不能当作通过。
func (r *SellabilityReport) Sellable() bool {
	return r != nil && r.Verdict == VerdictSellable
}

// ClassifySellability 纯函数：由买卖事件数给出结论。
//
// 独立成纯函数的目的：判定口径可以被单测穷举，且未来换数据源时判定逻辑不变。
func ClassifySellability(buys, sells, minSamples int) (SellabilityVerdict, string) {
	if minSamples <= 0 {
		minSamples = MinSellSamples
	}
	if buys < 0 {
		buys = 0
	}
	if sells < 0 {
		sells = 0
	}

	if sells > 0 {
		return VerdictSellable, fmt.Sprintf("链上存在 %d 笔卖出（买入 %d 笔）", sells, buys)
	}
	if buys >= minSamples {
		return VerdictNoSellPath, fmt.Sprintf("仅有 %d 笔买入、0 笔卖出（买入样本已达阈值 %d）", buys, minSamples)
	}
	return VerdictUnknown, fmt.Sprintf("样本不足（买入 %d 笔 < 阈值 %d）", buys, minSamples)
}

// TransferStats 某代币与某池子在区间内的转账方向统计。
type TransferStats struct {
	Buys  int // 池子 → 外部（用户买入）
	Sells int // 外部 → 池子（用户卖出）
}

// TransferCounter 统计代币与池子之间的转账方向（由调用方注入具体 RPC 实现，便于测试与换链）。
type TransferCounter interface {
	// CountTransfers 统计 [fromBlock, toBlock] 内 token 与 pool 之间的转账笔数。
	CountTransfers(ctx context.Context, chainID, token, pool string, fromBlock, toBlock uint64) (TransferStats, error)
}

// SellabilityProbe 完整探针能力：既能统计方向，也能给出判定报告。
// Security 依赖它；只实现 TransferCounter 的替身无法满足（避免"有统计没判定"的半成品注入）。
type SellabilityProbe interface {
	TransferCounter
	Assess(ctx context.Context, chainID, token, pool string, lookback uint64) (*SellabilityReport, error)
}

// EVMTransferCounter 基于 eth_getLogs 的 EVM 实现（复用 RPCPool 的多节点容错）。
type EVMTransferCounter struct {
	rpc *chain.RPCPool
}

// NewEVMTransferCounter 构建计数器。
func NewEVMTransferCounter(name string, rpcURLs []string) *EVMTransferCounter {
	return &EVMTransferCounter{rpc: chain.NewRPCPool(name, rpcURLs)}
}

// transferTopic0 = keccak256("Transfer(address,address,uint256)")。
var transferTopic0Hex = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

// CountTransfers 实现 TransferCounter。
//
// 实现要点：分两次查询（按 from=pool 与 to=pool 过滤），只看**是否发生**与**方向**，
// 不解析金额——金额级解析已由链适配器负责，这里只需要"有没有这条路径"的证据。
func (c *EVMTransferCounter) CountTransfers(ctx context.Context, chainID, token, pool string, fromBlock, toBlock uint64) (TransferStats, error) {
	if c == nil || c.rpc == nil {
		return TransferStats{}, fmt.Errorf("sellability: RPC 未配置")
	}
	if !isHexAddress(token) || !isHexAddress(pool) {
		return TransferStats{}, fmt.Errorf("sellability: 地址非法")
	}

	buys, err := c.countLogs(ctx, token, []any{transferTopic0Hex, padTopicHex(pool), nil}, fromBlock, toBlock)
	if err != nil {
		return TransferStats{}, err
	}
	sells, err := c.countLogs(ctx, token, []any{transferTopic0Hex, nil, padTopicHex(pool)}, fromBlock, toBlock)
	if err != nil {
		return TransferStats{}, err
	}
	return TransferStats{Buys: buys, Sells: sells}, nil
}

func (c *EVMTransferCounter) countLogs(ctx context.Context, token string, topics []any, fromBlock, toBlock uint64) (int, error) {
	query := map[string]any{
		"fromBlock": hexUint64(fromBlock),
		"toBlock":   hexUint64(toBlock),
		"address":   token,
		"topics":    topics,
	}
	var raw []struct {
		TxHash string `json:"transactionHash"`
	}
	if err := c.rpc.Call(ctx, "eth_getLogs", []any{query}, &raw); err != nil {
		return 0, fmt.Errorf("sellability: eth_getLogs 失败: %w", err)
	}
	return len(raw), nil
}

// BlockchainHeight 当前区块高度（供调用方计算回看区间）。
func (c *EVMTransferCounter) BlockchainHeight(ctx context.Context) (uint64, error) {
	if c == nil || c.rpc == nil {
		return 0, fmt.Errorf("sellability: RPC 未配置")
	}
	var raw string
	if err := c.rpc.Call(ctx, "eth_blockNumber", []any{}, &raw); err != nil {
		return 0, err
	}
	return parseHexUint64(raw)
}

// Assess 组合"当前高度 + 回看区间 + 方向统计 + 判定"。
func (c *EVMTransferCounter) Assess(ctx context.Context, chainID, token, pool string, lookback uint64) (*SellabilityReport, error) {
	if lookback == 0 {
		lookback = DefaultSellLookbackBlocks
	}
	head, err := c.BlockchainHeight(ctx)
	if err != nil {
		return nil, err
	}
	from := uint64(0)
	if head > lookback {
		from = head - lookback
	}
	return assessWithCounter(ctx, c, chainID, token, pool, from, head)
}

// assessWithCounter 抽出"统计 → 判定 → 组装报告"的组合逻辑，
// 使判定链路可用 stub 计数器单测，不必连真实 RPC。
func assessWithCounter(ctx context.Context, counter TransferCounter, chainID, token, pool string, fromBlock, toBlock uint64) (*SellabilityReport, error) {
	if counter == nil {
		return nil, fmt.Errorf("sellability: 计数器为空")
	}
	stats, err := counter.CountTransfers(ctx, chainID, token, pool, fromBlock, toBlock)
	if err != nil {
		return nil, fmt.Errorf("sellability: 统计转账失败: %w", err)
	}

	verdict, evidence := ClassifySellability(stats.Buys, stats.Sells, MinSellSamples)
	total := stats.Buys + stats.Sells
	ratio := 0.0
	if total > 0 {
		ratio = float64(stats.Sells) / float64(total)
	}
	return &SellabilityReport{
		Chain: chainID, Token: token, Pool: pool,
		Verdict: verdict, Buys: stats.Buys, Sells: stats.Sells, SellRatio: ratio,
		Evidence: evidence, Source: "onchain_transfer_history", CheckedAt: time.Now().UTC(),
	}, nil
}

// ---- 轻量 hex/地址工具（避免把 chain/base 的实现细节引进来）----

func isHexAddress(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 42 || !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return false
	}
	for _, r := range s[2:] {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func padTopicHex(addr string) string {
	clean := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(addr), "0x"), "0X")
	return "0x" + strings.Repeat("0", 24) + strings.ToLower(clean)
}

func hexUint64(v uint64) string {
	if v == 0 {
		return "0x0"
	}
	const digits = "0123456789abcdef"
	var buf [16]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = digits[v&0xf]
		v >>= 4
	}
	return "0x" + string(buf[i:])
}

func parseHexUint64(s string) (uint64, error) {
	clean := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "0x"), "0X")
	if clean == "" {
		return 0, fmt.Errorf("sellability: 空区块高度")
	}
	v, ok := new(big.Int).SetString(clean, 16)
	if !ok || !v.IsUint64() {
		return 0, fmt.Errorf("sellability: 区块高度非法 %q", s)
	}
	return v.Uint64(), nil
}
