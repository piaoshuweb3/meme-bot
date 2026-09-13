package model

import "time"

// ---------------------------------------------------------------------------
// 信号（自主漏斗 + 跟单 + Agent）
// ---------------------------------------------------------------------------

// SignalSource 信号来源。
type SignalSource string

const (
	// SourceFollow 聪明钱跟单信号。
	SourceFollow SignalSource = "follow"
	// SourceAutonomous 自主漏斗信号（流动性/成交量突增 + 入场确认）。
	SourceAutonomous SignalSource = "autonomous"
	// SourceAgent LLM Agent 生成的信号。
	SourceAgent SignalSource = "agent"
)

// SignalStatus 信号生命周期状态。
type SignalStatus string

const (
	SignalPending   SignalStatus = "pending"   // 已生成，等待确认条件
	SignalConfirmed SignalStatus = "confirmed" // 确认通过，可进入风控
	SignalExpired   SignalStatus = "expired"   // 超时衰减作废
	SignalRejected  SignalStatus = "rejected"  // 被风控/安全过滤拒绝
	SignalExecuted  SignalStatus = "executed"  // 已执行
)

// Signal 交易信号。
type Signal struct {
	ID             string         `json:"id"`
	Chain          string         `json:"chain"`
	Token          string         `json:"token"`
	TokenSymbol    string         `json:"token_symbol"`
	Source         SignalSource   `json:"source"`
	TriggerAddress string         `json:"trigger_address,omitempty"`
	TriggerScore   float64        `json:"trigger_score,omitempty"`
	AmountUSD      float64        `json:"amount_usd"`
	PriceUSD       float64        `json:"price_usd"`
	LiquidityUSD   float64        `json:"liquidity_usd"`
	VolumeSpike    float64        `json:"volume_spike"`
	Security       SecurityReport `json:"security"`
	// Decay 衰减系数：1.0 全新 → 0 完全失效
	Decay       float64      `json:"decay"`
	Status      SignalStatus `json:"status"`
	Reason      string       `json:"reason,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	ExpiresAt   time.Time    `json:"expires_at"`
	ConfirmedAt time.Time    `json:"confirmed_at,omitempty"`
	// 跟单专用：需要满足的确认条件是否已经满足
	FollowConfirmed bool `json:"follow_confirmed"`
}

// SignalFilter 静态安全与流动性过滤阈值。
type SignalFilter struct {
	MaxBuyTax           float64
	MaxSellTax          float64
	MinLiquidityUSD     float64
	MinVolumeSpike      float64
	MinLPBurntPercent   float64
	RequireOpenSource   bool
	RejectHoneypot      bool
	RejectMintable      bool
	RejectWithBlacklist bool
}

// FilterResult 过滤结果。
type FilterResult struct {
	Passed bool     `json:"passed"`
	Reason string   `json:"reason,omitempty"`
	Checks []string `json:"checks,omitempty"`
}

// StrategyPort 策略层对外端口（供 API / Agent 调用）。
type StrategyPort interface {
	// Evaluate 评估一次候选机会并产出信号（可能返回 nil 表示被过滤）。
	Evaluate(ev SwapEvent) (*Signal, error)
	// Confirm 尝试确认信号（跟风确认 / 入场确认）。
	Confirm(sig *Signal) (*Signal, error)
	// Active 当前活跃信号。
	Active() []*Signal
}
