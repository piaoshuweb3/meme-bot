// Package risk 实现风控引擎：所有执行路径（含 LLM Agent 发起）的唯一关卡。
//
// 覆盖文书要求的全部硬约束：
//   - 单币仓位上限、最大同时持仓数
//   - 硬止损（价格）+ 流动性骤降双触发
//   - 移动止盈（Trailing Stop）
//   - 每日最大亏损 → 强制冷却
//   - 人工熔断（一键暂停，执行层每单校验）
package risk

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"meme-bot/internal/config"
	"meme-bot/internal/metrics"
	"meme-bot/internal/model"
)

// StateStore 风控状态存储（熔断标志、冷却、当日亏损、权益）。
//
// 抽象出来是为了：生产用 Redis（多实例共享），测试用内存实现。
type StateStore interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Incr(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error)
	Del(ctx context.Context, key string) error
}

// Engine 风控引擎。
type Engine struct {
	cfg       config.RiskConfig
	store     StateStore
	positions model.PositionStore
	metrics   *metrics.Registry
	log       *zap.Logger
	// equityUSD 组合权益（美元），用于仓位百分比换算
	equityMu  sync.RWMutex
	equityUSD float64
}

// New 构建风控引擎。positions 与 metrics 可为 nil（功能降级）。
func New(cfg config.RiskConfig, store StateStore, positions model.PositionStore, reg *metrics.Registry, log *zap.Logger) *Engine {
	if store == nil {
		store = NewMemoryStore()
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Engine{
		cfg:       cfg,
		store:     store,
		positions: positions,
		metrics:   reg,
		log:       log,
		equityUSD: 10000, // 默认 1 万美金基准，可由 SetEquity 更新
	}
}

var _ model.RiskPort = (*Engine)(nil)

// SetEquity 更新组合权益（影响单币仓位上限）。
func (e *Engine) SetEquity(usd float64) {
	e.equityMu.Lock()
	e.equityUSD = usd
	e.equityMu.Unlock()
	if e.metrics != nil {
		e.metrics.Set("memebot_equity_usd", usd)
	}
}

func (e *Engine) equity() float64 {
	e.equityMu.RLock()
	defer e.equityMu.RUnlock()
	if e.equityUSD <= 0 {
		return 10000
	}
	return e.equityUSD
}

// ---- 键名约定 ----
const (
	keyPaused       = "risk:paused"
	keyPauseReason  = "risk:paused:reason"
	keyDailyLoss    = "risk:daily_loss_usd"
	keyCooldownBase = "risk:cooldown"
)

func cooldownKey(chain, token string) string {
	return keyCooldownBase + ":" + strings.ToLower(chain) + ":" + strings.ToLower(token)
}

// Pause 人工/自动熔断。
func (e *Engine) Pause(reason string, until time.Time) error {
	ctx := context.Background()
	ttl := time.Until(until)
	if until.IsZero() || ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if err := e.store.Set(ctx, keyPaused, "1", ttl); err != nil {
		return err
	}
	if err := e.store.Set(ctx, keyPauseReason, reason, ttl); err != nil {
		return err
	}
	if e.metrics != nil {
		e.metrics.Set("memebot_paused", 1)
	}
	e.log.Warn("risk engine paused", zap.String("reason", reason), zap.Duration("ttl", ttl))
	return nil
}

// Resume 解除熔断。
func (e *Engine) Resume() error {
	ctx := context.Background()
	if err := e.store.Del(ctx, keyPaused); err != nil {
		return err
	}
	if err := e.store.Del(ctx, keyPauseReason); err != nil {
		return err
	}
	if e.metrics != nil {
		e.metrics.Set("memebot_paused", 0)
	}
	e.log.Info("risk engine resumed")
	return nil
}

// Paused 查询熔断状态。
func (e *Engine) Paused() (bool, string) {
	ctx := context.Background()
	v, ok, err := e.store.Get(ctx, keyPaused)
	if err != nil || !ok || v != "1" {
		return false, ""
	}
	reason, _, _ := e.store.Get(ctx, keyPauseReason)
	return true, reason
}

// CheckEntry 入场风控（实现 model.RiskPort）。
func (e *Engine) CheckEntry(req model.EntryRequest) (*model.RiskDecision, error) {
	ctx := context.Background()

	if paused, reason := e.Paused(); paused {
		return &model.RiskDecision{Verdict: model.VerdictReject, Reason: "系统熔断中：" + reason}, nil
	}

	// 冷却期
	if _, ok, _ := e.store.Get(ctx, cooldownKey(req.Chain, req.Token)); ok {
		return &model.RiskDecision{Verdict: model.VerdictReject, Reason: "该代币处于冷却期"}, nil
	}

	// 最小流动性（防止在薄池子里买入）
	if req.LiquidityUSD > 0 && req.LiquidityUSD < 15000 {
		return &model.RiskDecision{Verdict: model.VerdictReject, Reason: "流动性过低"}, nil
	}

	// 滑点上限
	slippage := req.SlippageBps
	if slippage <= 0 {
		slippage = 100
	}
	if slippage > e.cfg.MaxSlippageBps {
		slippage = e.cfg.MaxSlippageBps
	}

	// 同时持仓数量
	if e.positions != nil {
		open, err := e.positions.ListOpen(req.Chain)
		if err != nil {
			return nil, fmt.Errorf("risk: list positions: %w", err)
		}
		if len(open) >= e.cfg.MaxOpenPositions {
			return &model.RiskDecision{Verdict: model.VerdictReject, Reason: "已达最大同时持仓数"}, nil
		}
		// 已持有该代币：不再加仓
		for _, p := range open {
			if strings.EqualFold(p.Token, req.Token) {
				return &model.RiskDecision{Verdict: model.VerdictReject, Reason: "已持有该代币"}, nil
			}
		}
	}

	// 当日亏损上限
	if loss, ok, _ := e.store.Get(ctx, keyDailyLoss); ok {
		if v, err := strconv.ParseFloat(loss, 64); err == nil {
			limit := e.equity() * e.cfg.DailyLossLimitPct
			if v >= limit {
				_ = e.Pause("当日亏损达到上限", time.Now().Add(time.Duration(e.cfg.CooldownAfterLossMinutes)*time.Minute))
				return &model.RiskDecision{Verdict: model.VerdictReject, Reason: "当日亏损达上限，已熔断"}, nil
			}
		}
	}

	// 单币仓位上限
	maxPerToken := e.equity() * e.cfg.MaxPositionPct
	amount := req.AmountUSD
	verdict := model.VerdictAllow
	reason := ""
	if amount > maxPerToken {
		amount = maxPerToken
		verdict = model.VerdictReduce
		reason = "单币仓位超限，已按上限缩减"
	}

	split := e.cfg.SplitEntries
	if split <= 0 {
		split = 1
	}

	return &model.RiskDecision{
		Verdict:      verdict,
		Reason:       reason,
		AllowedUSD:   amount,
		SlippageBps:  slippage,
		SplitEntries: split,
	}, nil
}

// CheckExit 出场风控：硬止损 / 流动性骤降 / 移动止盈（实现 model.RiskPort）。
func (e *Engine) CheckExit(p *model.Position, markPriceUSD, markLiquidityUSD float64) (*model.RiskDecision, error) {
	if p == nil {
		return nil, errors.New("risk: nil position")
	}
	if p.EntryPriceUSD <= 0 {
		return &model.RiskDecision{Verdict: model.VerdictReject, Reason: "缺少入场价格"}, nil
	}

	// 1) 硬止损
	stopPrice := p.EntryPriceUSD * (1 - e.pick(p.StopLossPct, e.cfg.StopLossPct))
	if markPriceUSD > 0 && markPriceUSD <= stopPrice {
		return &model.RiskDecision{
			Verdict: model.VerdictAllow,
			Reason:  fmt.Sprintf("触发硬止损：现价 %.8f ≤ 止损价 %.8f", markPriceUSD, stopPrice),
		}, nil
	}

	// 2) 流动性骤降
	if p.LiquidityAtEntryUSD > 0 && markLiquidityUSD > 0 {
		threshold := p.LiquidityAtEntryUSD * (1 - e.cfg.LiquidityDropTriggerPct)
		if markLiquidityUSD <= threshold {
			return &model.RiskDecision{
				Verdict: model.VerdictAllow,
				Reason:  fmt.Sprintf("流动性骤降：%.0f → %.0f USD", p.LiquidityAtEntryUSD, markLiquidityUSD),
			}, nil
		}
	}

	// 3) 移动止盈
	peak := p.PeakPriceUSD
	if markPriceUSD > peak {
		peak = markPriceUSD
	}
	trail := peak * (1 - e.pick(p.TrailingStopPct, e.cfg.TrailingStopPct))
	if peak > p.EntryPriceUSD && markPriceUSD > 0 && markPriceUSD <= trail {
		return &model.RiskDecision{
			Verdict: model.VerdictAllow,
			Reason:  fmt.Sprintf("移动止盈：峰值 %.8f 回落至 %.8f", peak, markPriceUSD),
		}, nil
	}

	return &model.RiskDecision{Verdict: model.VerdictReject, Reason: "继续持有"}, nil
}

func (e *Engine) pick(v, fallback float64) float64 {
	if v > 0 {
		return v
	}
	return fallback
}

// RecordLoss 记录已实现亏损（触发日亏损限额判断）。
func (e *Engine) RecordLoss(chain, token string, lossUSD float64, cooldown time.Duration) {
	ctx := context.Background()
	if lossUSD > 0 {
		_, _ = e.store.Incr(ctx, keyDailyLoss, lossUSD, 24*time.Hour)
	}
	if cooldown <= 0 {
		cooldown = time.Duration(e.cfg.CooldownAfterLossMinutes) * time.Minute
	}
	_ = e.store.Set(ctx, cooldownKey(chain, token), "1", cooldown)
}

// RecordWin 记录盈利（仅用于统计，不影响冷却）。
func (e *Engine) RecordWin(profitUSD float64) {
	if profitUSD <= 0 {
		return
	}
	ctx := context.Background()
	_, _ = e.store.Incr(ctx, "risk:realized_profit_usd", profitUSD, 7*24*time.Hour)
}
