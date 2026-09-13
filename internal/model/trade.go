package model

import (
	"math/big"
	"time"
)

// ---------------------------------------------------------------------------
// 订单、持仓、执行结果
// ---------------------------------------------------------------------------

// OrderStatus 订单状态机。
//
//	Pending → Submitted → Confirming → Filled
//	                  ↘ Failed / Partial → Compensated
type OrderStatus string

const (
	OrderPending     OrderStatus = "pending"
	OrderSubmitted   OrderStatus = "submitted"
	OrderConfirming  OrderStatus = "confirming"
	OrderFilled      OrderStatus = "filled"
	OrderPartial     OrderStatus = "partial"
	OrderFailed      OrderStatus = "failed"
	OrderCompensated OrderStatus = "compensated"
)

// Terminal 是否为终态。
func (s OrderStatus) Terminal() bool {
	switch s {
	case OrderFilled, OrderFailed, OrderCompensated, OrderPartial:
		return true
	}
	return false
}

// Order 交易订单。
type Order struct {
	ID          string      `json:"id"`
	Chain       string      `json:"chain"`
	TokenIn     string      `json:"token_in"`
	TokenOut    string      `json:"token_out"`
	TokenSymbol string      `json:"token_symbol,omitempty"`
	Side        Side        `json:"side"`
	AmountIn    *big.Int    `json:"amount_in"`
	AmountOut   *big.Int    `json:"amount_out,omitempty"`
	Status      OrderStatus `json:"status"`
	TxHash      string      `json:"tx_hash,omitempty"`
	Router      string      `json:"router,omitempty"`
	IsPrivate   bool        `json:"is_private"`
	SlippageBps int         `json:"slippage_bps"`
	Attempts    int         `json:"attempts"`
	SignalID    string      `json:"signal_id,omitempty"`
	DryRun      bool        `json:"dry_run"`
	Error       string      `json:"error,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// PositionStatus 持仓状态。
type PositionStatus string

const (
	PositionOpen   PositionStatus = "open"
	PositionClosed PositionStatus = "closed"
)

// Position 持仓。
type Position struct {
	ID                  string         `json:"id"`
	Chain               string         `json:"chain"`
	Token               string         `json:"token"`
	TokenSymbol         string         `json:"token_symbol"`
	Amount              *big.Int       `json:"amount"`
	Decimals            uint8          `json:"decimals"`
	EntryPriceUSD       float64        `json:"entry_price_usd"`
	CurrentPriceUSD     float64        `json:"current_price_usd"`
	PeakPriceUSD        float64        `json:"peak_price_usd"`
	InvestedUSD         float64        `json:"invested_usd"`
	StopLossPct         float64        `json:"stop_loss_pct"`
	TrailingStopPct     float64        `json:"trailing_stop_pct"`
	LiquidityAtEntryUSD float64        `json:"liquidity_at_entry_usd"`
	CurrentLiquidityUSD float64        `json:"current_liquidity_usd"`
	Status              PositionStatus `json:"status"`
	ExitPriceUSD        float64        `json:"exit_price_usd,omitempty"`
	RealizedPnLUSD      float64        `json:"realized_pnl_usd,omitempty"`
	SignalID            string         `json:"signal_id,omitempty"`
	DryRun              bool           `json:"dry_run"`
	OpenedAt            time.Time      `json:"opened_at"`
	ClosedAt            time.Time      `json:"closed_at,omitempty"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

// EntryRequest 入场风控请求。
type EntryRequest struct {
	Chain        string       `json:"chain"`
	Token        string       `json:"token"`
	SignalID     string       `json:"signal_id,omitempty"`
	Source       SignalSource `json:"source"`
	Address      string       `json:"address,omitempty"`
	AmountUSD    float64      `json:"amount_usd"`
	PriceUSD     float64      `json:"price_usd"`
	LiquidityUSD float64      `json:"liquidity_usd"`
	SlippageBps  int          `json:"slippage_bps"`
	RequestedAt  time.Time    `json:"requested_at"`
}

// RiskVerdict 风控裁决。
type RiskVerdict string

const (
	VerdictAllow  RiskVerdict = "allow"
	VerdictReduce RiskVerdict = "reduce"
	VerdictReject RiskVerdict = "reject"
)

// RiskDecision 风控裁决结果。
type RiskDecision struct {
	Verdict       RiskVerdict `json:"verdict"`
	Reason        string      `json:"reason,omitempty"`
	AllowedUSD    float64     `json:"allowed_usd"`
	SlippageBps   int         `json:"slippage_bps"`
	SplitEntries  int         `json:"split_entries"`
	CooldownUntil time.Time   `json:"cooldown_until,omitempty"`
}

// RiskPort 风控端口：所有执行（含 Agent 发起）必经此处。
type RiskPort interface {
	CheckEntry(req EntryRequest) (*RiskDecision, error)
	// CheckExit 判定是否触发止损/止盈（硬止损 + 流动性骤降 + 移动止盈）。
	CheckExit(p *Position, markPriceUSD, markLiquidityUSD float64) (*RiskDecision, error)
	// Pause 人工熔断（全局或指定范围）。
	Pause(reason string, until time.Time) error
	// Paused 当前是否处于熔断状态。
	Paused() (bool, string)
}

// ExecutionResult 执行结果。
type ExecutionResult struct {
	Order        *Order      `json:"order"`
	TxHash       string      `json:"tx_hash,omitempty"`
	Status       OrderStatus `json:"status"`
	FilledAmount *big.Int    `json:"filled_amount,omitempty"`
	AvgPriceUSD  float64     `json:"avg_price_usd,omitempty"`
	SlippageBps  int         `json:"slippage_bps,omitempty"`
	DryRun       bool        `json:"dry_run"`
	Error        string      `json:"error,omitempty"`
}

// TradeIntent 交易意图（风控通过后交给执行引擎）。
type TradeIntent struct {
	Chain       string    `json:"chain"`
	TokenIn     string    `json:"token_in"`
	TokenOut    string    `json:"token_out"`
	AmountIn    *big.Int  `json:"amount_in"`
	Side        Side      `json:"side"`
	SlippageBps int       `json:"slippage_bps"`
	SignalID    string    `json:"signal_id,omitempty"`
	PositionID  string    `json:"position_id,omitempty"`
	OrderID     string    `json:"order_id"`
	DryRun      bool      `json:"dry_run"`
	Reason      string    `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ExecutorPort 执行端口。
type ExecutorPort interface {
	// Execute 执行一笔交易意图，内部驱动订单状态机。
	Execute(intent *TradeIntent) (*ExecutionResult, error)
	// MarkPrice 更新持仓标记价格并触发风控退出检查。
	MarkPrice(p *Position, priceUSD, liquidityUSD float64) (*RiskDecision, error)
}

// PositionStore 持仓仓储端口。
type PositionStore interface {
	Open(p *Position) error
	Update(p *Position) error
	Get(id string) (*Position, error)
	ListOpen(chain string) ([]*Position, error)
	Close(id string, exitPriceUSD float64, realizedPnLUSD float64) error
}
