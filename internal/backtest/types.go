// Package backtest 提供历史事件回放引擎：用真实或合成事件流验证策略表现。
//
// 定位（对齐文书"可测试性"要求）：
//   - 复用**同一套**策略漏斗与风控引擎（不复制逻辑），仅把时钟与执行替换为回放/模拟；
//   - 输出可复核的指标（胜率、盈亏比、最大回撤、费用）+ 逐笔成交与权益曲线；
//   - 默认保守参数（滑点/手续费/止损）使结果接近真实约束。
package backtest

import "time"

// Config 回测参数。
type Config struct {
	// InitialEquityUSD 初始权益（美元）。
	InitialEquityUSD float64 `json:"initial_equity_usd"`
	// SlippageBps 成交滑点（bps）：入场与出场均按不利方向计入。
	SlippageBps int `json:"slippage_bps"`
	// FeeBps 手续费（bps）：入场与出场各收一次。
	FeeBps int `json:"fee_bps"`
	// MaxHoldingMinutes 最大持仓时长（分钟，0 表示不限制）。
	MaxHoldingMinutes int `json:"max_holding_minutes"`
	// StopLossPct 止损阈值（0 → 用风控引擎配置）。
	StopLossPct float64 `json:"stop_loss_pct"`
	// TakeProfitPct 止盈阈值（0 表示不启用独立止盈）。
	TakeProfitPct float64 `json:"take_profit_pct"`
	// MinSignalAmountUSD 只处理金额不低于该值的信号（0 = 不过滤）。
	MinSignalAmountUSD float64 `json:"min_signal_amount_usd"`
	// FollowConfirm 是否要求跟风确认后才入场（与实盘一致时为 true）。
	FollowConfirm bool `json:"follow_confirm"`
}

// DefaultConfig 返回保守的默认参数。
func DefaultConfig() Config {
	return Config{
		InitialEquityUSD:  10000,
		SlippageBps:       150,
		FeeBps:            30,
		MaxHoldingMinutes: 240,
		StopLossPct:       0.25,
		TakeProfitPct:     0.60,
		FollowConfirm:     true,
	}
}

// Trade 一笔模拟成交（入场 + 出场配对）。
type Trade struct {
	Token      string    `json:"token"`
	Chain      string    `json:"chain"`
	SignalID   string    `json:"signal_id,omitempty"`
	Source     string    `json:"source,omitempty"`
	EntryAt    time.Time `json:"entry_at"`
	EntryPrice float64   `json:"entry_price"`
	ExitAt     time.Time `json:"exit_at"`
	ExitPrice  float64   `json:"exit_price"`
	ExitReason string    `json:"exit_reason"`
	AmountUSD  float64   `json:"amount_usd"`
	PnLUSD     float64   `json:"pnl_usd"`
	ReturnPct  float64   `json:"return_pct"`
	HoldingMin float64   `json:"holding_minutes"`
	FeesUSD    float64   `json:"fees_usd"`
}

// EquityPoint 权益曲线点。
type EquityPoint struct {
	At        time.Time `json:"at"`
	EquityUSD float64   `json:"equity_usd"`
}

// Metrics 回测指标。
type Metrics struct {
	Events         int     `json:"events"`
	Signals        int     `json:"signals"`
	Symbols        int     `json:"symbols"`
	Trades         int     `json:"trades"`
	Wins           int     `json:"wins"`
	Losses         int     `json:"losses"`
	WinRate        float64 `json:"win_rate"`
	ProfitFactor   float64 `json:"profit_factor"`
	TotalPnLUSD    float64 `json:"total_pnl_usd"`
	ReturnPct      float64 `json:"return_pct"`
	MaxDrawdown    float64 `json:"max_drawdown"`
	FinalEquityUSD float64 `json:"final_equity_usd"`
	AvgHoldingMin  float64 `json:"avg_holding_minutes"`
	AvgWinUSD      float64 `json:"avg_win_usd"`
	AvgLossUSD     float64 `json:"avg_loss_usd"`
	FeesPaidUSD    float64 `json:"fees_paid_usd"`
	RejectedByRisk int     `json:"rejected_by_risk"`
}

// Report 回测报告。
type Report struct {
	Config  Config        `json:"config"`
	FirstAt time.Time     `json:"first_event_at"`
	LastAt  time.Time     `json:"last_event_at"`
	RunAt   time.Time     `json:"run_at"`
	Metrics Metrics       `json:"metrics"`
	Trades  []Trade       `json:"trades"`
	Equity  []EquityPoint `json:"equity"`
	Notes   []string      `json:"notes,omitempty"`
}
