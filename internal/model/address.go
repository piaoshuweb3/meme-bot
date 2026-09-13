package model

import (
	"context"
	"time"
)

// ---------------------------------------------------------------------------
// 地址画像（跟单筛选）
// ---------------------------------------------------------------------------

// AddressTag 地址标签。
type AddressTag string

const (
	TagSmartMoney AddressTag = "smart_money"
	TagWhale      AddressTag = "whale"
	TagSniper     AddressTag = "sniper"
	TagCEX        AddressTag = "cex"
	TagMixer      AddressTag = "mixer"
	TagBot        AddressTag = "bot"
	TagTeam       AddressTag = "team"
	TagSuspect    AddressTag = "suspect"
)

// AddressProfile 地址画像（滚动窗口统计 + 实时评分）。
type AddressProfile struct {
	Address         string       `json:"address"`
	Chain           string       `json:"chain"`
	WinRate         float64      `json:"win_rate"`         // 0-1
	ProfitFactor    float64      `json:"profit_factor"`    // 平均盈利/平均亏损
	MaxDrawdown     float64      `json:"max_drawdown"`     // 0-1
	AvgHoldSeconds  int64        `json:"avg_hold_seconds"` // 平均持仓时长
	Consistency     float64      `json:"consistency"`      // 0-1 盈利分布均匀度
	SizeConsistency float64      `json:"size_consistency"` // 0-1 单笔金额稳定性
	MedianBuyUSD    float64      `json:"median_buy_usd"`   // 历史买入金额中位数（大额买入判定基准）
	TotalTrades     int          `json:"total_trades"`
	WinningTrades   int          `json:"winning_trades"`
	RecentScore     float64      `json:"recent_score"` // 综合评分 0-1
	Tags            []AddressTag `json:"tags"`
	IsBlacklisted   bool         `json:"is_blacklisted"`
	BlacklistReason string       `json:"blacklist_reason,omitempty"`
	LastActive      time.Time    `json:"last_active"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

// ScoreInput 评分所需的滚动统计原始量。
type ScoreInput struct {
	WinRate         float64
	ProfitFactor    float64
	MaxDrawdown     float64
	Consistency     float64
	LastActive      time.Time
	Now             time.Time
	RecencyHalfLife time.Duration
}

// TradeRecord 单笔交易记录（画像统计的原始数据）。
type TradeRecord struct {
	Chain     string    `json:"chain"`
	Address   string    `json:"address"`
	Token     string    `json:"token"`
	Side      Side      `json:"side"`
	AmountUSD float64   `json:"amount_usd"`
	PriceUSD  float64   `json:"price_usd"`
	TxHash    string    `json:"tx_hash"`
	PnLUSD    float64   `json:"pnl_usd"`
	At        time.Time `json:"at"`
}

// ProfileStore 地址画像仓储端口。
type ProfileStore interface {
	Get(ctx context.Context, chain, address string) (*AddressProfile, error)
	Upsert(ctx context.Context, p *AddressProfile) error
	ListTop(ctx context.Context, chain string, limit int) ([]*AddressProfile, error)
	IsBlacklisted(ctx context.Context, chain, address string) (bool, error)
	Blacklist(ctx context.Context, chain, address, reason string) error
	RecordTrade(ctx context.Context, rec *TradeRecord) error
	RecentTrades(ctx context.Context, chain, address string, since time.Time, limit int) ([]*TradeRecord, error)
}

// ScorerPort 地址评分端口。
type ScorerPort interface {
	// Score 计算综合评分（0-1）。
	Score(p *AddressProfile) float64
	// IsEligible 判断是否进入可跟单池。
	IsEligible(ctx context.Context, chain, address string) (eligible bool, score float64, err error)
	// IsLargeBuy 判断该笔买入是否为大额（相对历史中位数）。
	IsLargeBuy(ctx context.Context, chain, address string, amountUSD float64) (bool, error)
}
