// Package model 是 meme-bot 的领域契约包：跨模块共享的类型与端口（ports）接口。
//
// 设计约束（并行开发前提）：
//   - 任何跨模块调用只能使用本包定义的类型与接口；
//   - 各模块实现（chain / address / alert / strategy / risk / agent ...）只依赖本包；
//   - 本包不依赖任何其他 internal 包，避免循环依赖。
package model

import (
	"context"
	"math/big"
	"time"
)

// ---------------------------------------------------------------------------
// 基础市场类型
// ---------------------------------------------------------------------------

// Token 代币元数据。
type Token struct {
	Chain    string `json:"chain"`
	Address  string `json:"address"`
	Symbol   string `json:"symbol"`
	Decimals uint8  `json:"decimals"`
}

// Balance 余额（最小单位）。
type Balance struct {
	Amount   *big.Int `json:"amount"`
	Decimals uint8    `json:"decimals"`
}

// Price 价格快照。
type Price struct {
	Chain  string    `json:"chain"`
	Token  string    `json:"token"`
	USD    float64   `json:"usd"`
	At     time.Time `json:"at"`
	Source string    `json:"source"`
}

// GasEstimate Gas 估算。
type GasEstimate struct {
	GasLimit         uint64   `json:"gas_limit"`
	MaxFeePerGas     *big.Int `json:"max_fee_per_gas,omitempty"`
	MaxPriorityFee   *big.Int `json:"max_priority_fee,omitempty"`
	ComputeUnitPrice uint64   `json:"compute_unit_price,omitempty"`
	EstimatedCostUSD float64  `json:"estimated_cost_usd,omitempty"`
}

// UnsignedTx 未签名交易（跨链统一表示）。
type UnsignedTx struct {
	Chain    string   `json:"chain"`
	To       string   `json:"to"`
	Data     []byte   `json:"data"`
	Value    *big.Int `json:"value"`
	GasLimit uint64   `json:"gas_limit"`
	// Solana 专用：序列化后的交易体
	Serialized []byte `json:"-"`
	// 路由来源（1inch / jupiter / uniswap-router ...），用于审计
	Router string `json:"router"`
	// 是否为私有通道交易（不进公开 mempool）
	Private bool `json:"private"`
}

// TxReceipt 交易回执。
type TxReceipt struct {
	Chain       string    `json:"chain"`
	TxHash      string    `json:"tx_hash"`
	Status      uint64    `json:"status"` // 1 = success
	BlockNumber uint64    `json:"block_number"`
	GasUsed     uint64    `json:"gas_used"`
	At          time.Time `json:"at"`
}

// Side 买卖方向。
type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

// SwapParams 交易构建参数。
type SwapParams struct {
	Chain       string   `json:"chain"`
	TokenIn     string   `json:"token_in"`
	TokenOut    string   `json:"token_out"`
	AmountIn    *big.Int `json:"amount_in"`
	SlippageBps int      `json:"slippage_bps"`
	Recipient   string   `json:"recipient"`
	Deadline    int64    `json:"deadline"`
	// 优先走私有通道
	PreferPrivate bool `json:"prefer_private"`
}

// SecurityReport 合约安全报告（安全前置过滤的依据）。
type SecurityReport struct {
	Chain        string  `json:"chain"`
	Token        string  `json:"token"`
	IsHoneypot   bool    `json:"is_honeypot"`
	HasMint      bool    `json:"has_mint"`
	HasBlacklist bool    `json:"has_blacklist"`
	BuyTax       float64 `json:"buy_tax"`
	SellTax      float64 `json:"sell_tax"`
	Owner        string  `json:"owner"`
	IsOpenSource bool    `json:"is_open_source"`
	// 权限是否已放弃
	OwnershipRenounced bool      `json:"ownership_renounced"`
	Source             string    `json:"source"`
	CheckedAt          time.Time `json:"checked_at"`
	// 判定结论：危险镜像/可疑字段，由策略层统一阈值裁决
	Risky bool `json:"risky"`
}

// Concentration 持仓集中度。
type Concentration struct {
	Chain        string  `json:"chain"`
	Token        string  `json:"token"`
	Top10Percent float64 `json:"top10_percent"`
	Top20Percent float64 `json:"top20_percent"`
	HolderCount  int     `json:"holder_count"`
}

// LiquidityInfo 流动性信息（含 LP 锁定状态）。
type LiquidityInfo struct {
	Chain          string    `json:"chain"`
	Token          string    `json:"token"`
	PoolAddress    string    `json:"pool_address"`
	LiquidityUSD   float64   `json:"liquidity_usd"`
	Volume24hUSD   float64   `json:"volume_24h_usd"`
	LPBurntPercent float64   `json:"lp_burnt_percent"`
	LPLockedUntil  time.Time `json:"lp_locked_until"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// PoolInfo 池子信息。
type PoolInfo struct {
	Chain      string    `json:"chain"`
	Address    string    `json:"address"`
	Token0     string    `json:"token0"`
	Token1     string    `json:"token1"`
	ReserveUSD float64   `json:"reserve_usd"`
	CreatedAt  time.Time `json:"created_at"`
	AgeSeconds int64     `json:"age_seconds"`
}

// SwapEvent 链上 Swap 事件（事件驱动核心输入）。
type SwapEvent struct {
	Chain     string    `json:"chain"`
	TxHash    string    `json:"tx_hash"`
	Pool      string    `json:"pool"`
	TokenIn   string    `json:"token_in"`
	TokenOut  string    `json:"token_out"`
	AmountIn  *big.Int  `json:"amount_in"`
	AmountOut *big.Int  `json:"amount_out"`
	Sender    string    `json:"sender"`
	AmountUSD float64   `json:"amount_usd"`
	PriceUSD  float64   `json:"price_usd"`
	At        time.Time `json:"at"`
}

// Unsubscribe 取消订阅函数。
type Unsubscribe func()

// ChainAdapter 多链统一抽象：上层策略与执行只依赖本接口。
// 新增一条链 = 实现本接口 + 在 chain.Factory 注册。
type ChainAdapter interface {
	ChainID() string
	NativeToken() Token

	// 账户
	Balance(ctx context.Context, address, token string) (*Balance, error)

	// 交易
	BuildSwap(ctx context.Context, params SwapParams) (*UnsignedTx, error)
	EstimateGas(ctx context.Context, tx *UnsignedTx) (*GasEstimate, error)
	SendTransaction(ctx context.Context, signedTx string) (string, error)
	WaitConfirmation(ctx context.Context, txHash string, timeout time.Duration) (*TxReceipt, error)

	// 行情与池子
	TokenPrice(ctx context.Context, token string) (*Price, error)
	PoolInfo(ctx context.Context, pool string) (*PoolInfo, error)
	Liquidity(ctx context.Context, token string) (*LiquidityInfo, error)

	// 安全与链上数据
	TokenSecurity(ctx context.Context, token string) (*SecurityReport, error)
	HolderConcentration(ctx context.Context, token string, topN int) (*Concentration, error)

	// 事件
	SubscribeSwaps(ctx context.Context, token string, handler func(SwapEvent)) (Unsubscribe, error)
}

// MarketPort 行情与安全数据的只读端口（策略层消费的最小集合）。
type MarketPort interface {
	TokenPrice(ctx context.Context, chain, token string) (*Price, error)
	Liquidity(ctx context.Context, chain, token string) (*LiquidityInfo, error)
	Security(ctx context.Context, chain, token string) (*SecurityReport, error)
	Concentration(ctx context.Context, chain, token string, topN int) (*Concentration, error)
	Swaps(ctx context.Context, chain, token string, handler func(SwapEvent)) (Unsubscribe, error)
}
