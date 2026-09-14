// Package strategy 实现信号漏斗与执行引擎（对应文书的逻辑层与执行层）。
package strategy

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"meme-bot/internal/config"
	"meme-bot/internal/market"
	"meme-bot/internal/metrics"
	"meme-bot/internal/model"
)

// SignalEngine 自主信号漏斗 + 跟单信号（实现 model.StrategyPort）。
//
// 漏斗顺序（宁缺毋滥）：
//  1. 静态安全过滤（蜜罐 / mint / 黑名单 / 高税 / 未放弃权限）
//  2. 流动性门槛
//  3. 大额买入判定（相对该地址历史中位数）
//  4. 冷却窗口 + 信号 TTL 衰减
//  5. 跟风确认（窗口内出现独立后续买家）
type SignalEngine struct {
	cfg    config.SignalConfig
	filter model.SignalFilter
	market model.MarketPort
	scorer model.ScorerPort
	alerts model.AlertSink
	reg    *metrics.Registry
	log    *zap.Logger

	// now 可注入时钟：实盘为 time.Now；回测注入"当前事件时间"，使 TTL/冷却语义正确。
	// rolling 成交额滚动窗口：用于"相对该代币自身均值"的突增判定（比比值近似更可比）。
	rolling *market.RollingWindow
	now     func() time.Time

	mu         sync.Mutex
	active     map[string]*model.Signal
	buyers     map[string]map[string]time.Time
	lastSignal map[string]time.Time
}

// NewSignalEngine 构建信号引擎。
func NewSignalEngine(
	cfg config.SignalConfig,
	filter model.SignalFilter,
	market model.MarketPort,
	scorer model.ScorerPort,
	alerts model.AlertSink,
	reg *metrics.Registry,
	log *zap.Logger,
) *SignalEngine {
	if log == nil {
		log = zap.NewNop()
	}
	if cfg.TTLMinutes <= 0 {
		cfg.TTLMinutes = 30
	}
	if cfg.FollowConfirmWindowMinutes <= 0 {
		cfg.FollowConfirmWindowMinutes = 30
	}
	if cfg.LargeBuyMultiple <= 0 {
		cfg.LargeBuyMultiple = 2.0
	}
	return &SignalEngine{
		now:        time.Now,
		cfg:        cfg,
		filter:     filter,
		market:     market,
		scorer:     scorer,
		alerts:     alerts,
		reg:        reg,
		log:        log,
		active:     make(map[string]*model.Signal),
		buyers:     make(map[string]map[string]time.Time),
		lastSignal: make(map[string]time.Time),
	}
}

// WithRolling 注入成交额滚动窗口（可选；未注入时回退为"成交额/流动性"比值近似）。
func (e *SignalEngine) WithRolling(w *market.RollingWindow) *SignalEngine {
	if w != nil {
		e.rolling = w
	}
	return e
}

// WithClock 注入时钟（回测场景：用事件时间驱动 TTL / 冷却 / 衰减）。
func (e *SignalEngine) WithClock(now func() time.Time) *SignalEngine {
	if now != nil {
		e.now = now
	}
	return e
}

var _ model.StrategyPort = (*SignalEngine)(nil)

// Evaluate 实现 model.StrategyPort：对一次 Swap 事件做漏斗判定。
func (e *SignalEngine) Evaluate(ev model.SwapEvent) (*model.Signal, error) {
	if e.reg != nil {
		e.reg.Inc("memebot_swaps_observed_total", 1)
	}

	// 只处理“买入”方向：TokenOut 是被买走的代币
	token := strings.TrimSpace(ev.TokenOut)
	if token == "" || ev.AmountUSD <= 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1) 静态安全过滤
	security, err := e.market.Security(ctx, ev.Chain, token)
	if err != nil {
		e.log.Debug("security check failed", zap.String("token", token), zap.Error(err))
		return nil, nil
	}
	if pass, reason := e.securityPass(security); !pass {
		e.recordFiltered(token, reason)
		return nil, nil
	}
	// securityPass 在关闭保守拒绝时会放行 nil 报告；此处补占位报告，
	// 否则下方 `Security: *security` 将解引用空指针并导致进程 panic（已由单测覆盖）。
	if security == nil {
		security = &model.SecurityReport{Chain: ev.Chain, Token: token, Source: "unavailable"}
	}

	// 2) 流动性门槛
	liquidity, err := e.market.Liquidity(ctx, ev.Chain, token)
	if err != nil {
		return nil, nil
	}
	if e.filter.MinLiquidityUSD > 0 && liquidity.LiquidityUSD < e.filter.MinLiquidityUSD {
		e.recordFiltered(token, fmt.Sprintf("流动性不足 %.0f < %.0f", liquidity.LiquidityUSD, e.filter.MinLiquidityUSD))
		return nil, nil
	}

	// 3) 大额买入判定（跟单信号的前提）
	large := false
	if e.scorer != nil && ev.Sender != "" {
		if ok, err := e.scorer.IsLargeBuy(ctx, ev.Chain, ev.Sender, ev.AmountUSD); err == nil {
			large = ok
		} else {
			e.log.Debug("large buy check failed", zap.Error(err))
		}
	}

	// 4) 冷却窗口
	e.mu.Lock()
	if last, ok := e.lastSignal[token]; ok && e.cfg.CooldownMinutes > 0 {
		// 用注入时钟判断冷却：回测按事件时间推进，避免 wall clock 让冷却永不过期
		if e.now().UTC().Sub(last) < time.Duration(e.cfg.CooldownMinutes)*time.Minute {
			e.mu.Unlock()
			return nil, nil
		}
	}
	e.mu.Unlock()

	now := e.now().UTC()
	sig := &model.Signal{
		ID:             uuid.NewString(),
		Chain:          ev.Chain,
		Token:          token,
		TokenSymbol:    token[:min(len(token), 12)],
		Source:         model.SourceFollow,
		TriggerAddress: ev.Sender,
		AmountUSD:      ev.AmountUSD,
		PriceUSD:       ev.PriceUSD,
		LiquidityUSD:   liquidity.LiquidityUSD,
		VolumeSpike:    e.volumeMultipleFor(liquidity, ev.Chain, token, now),
		Security:       *security,
		Decay:          1.0,
		Status:         model.SignalPending,
		CreatedAt:      now,
		ExpiresAt:      now.Add(time.Duration(e.cfg.TTLMinutes) * time.Minute),
		Reason:         "聪明钱大额买入",
	}
	if !large {
		// 非大额买入：只记录买家（用于跟风确认），不生成信号
		e.trackBuyer(token, ev.Sender, now)
		return nil, nil
	}

	e.mu.Lock()
	e.active[sig.ID] = sig
	e.lastSignal[token] = now
	e.mu.Unlock()
	e.trackBuyer(token, ev.Sender, now)

	if e.reg != nil {
		e.reg.Inc("memebot_signals_total", 1)
	}
	e.log.Info("signal generated",
		zap.String("id", sig.ID), zap.String("chain", sig.Chain), zap.String("token", sig.Token),
		zap.Float64("amount_usd", sig.AmountUSD), zap.Float64("liquidity_usd", sig.LiquidityUSD))
	return sig, nil
}

// Confirm 实现 model.StrategyPort：确认信号（跟风确认 + TTL 检查）。
func (e *SignalEngine) Confirm(sig *model.Signal) (*model.Signal, error) {
	if sig == nil {
		return nil, fmt.Errorf("strategy: nil signal")
	}
	now := e.now().UTC()

	if now.After(sig.ExpiresAt) {
		sig.Status = model.SignalExpired
		sig.Decay = 0
		e.drop(sig.ID)
		return sig, nil
	}

	// 跟风确认：窗口内出现 ≥2 个独立买家（含触发者）
	window := time.Duration(e.cfg.FollowConfirmWindowMinutes) * time.Minute
	e.mu.Lock()
	buyers := e.buyers[sig.Token]
	distinct := 0
	for _, at := range buyers {
		if now.Sub(at) <= window {
			distinct++
		}
	}
	e.mu.Unlock()

	sig.FollowConfirmed = distinct >= 2
	if sig.FollowConfirmed {
		sig.Status = model.SignalConfirmed
		sig.ConfirmedAt = now
		sig.Decay = 1.0
	}
	return sig, nil
}

// Active 实现 model.StrategyPort：当前活跃信号。
func (e *SignalEngine) Active() []*model.Signal {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*model.Signal, 0, len(e.active))
	for _, s := range e.active {
		out = append(out, s)
	}
	return out
}

// Decay 扫描并衰减过期信号（worker 周期调用）。
func (e *SignalEngine) Decay(now time.Time) []*model.Signal {
	e.mu.Lock()
	defer e.mu.Unlock()

	var expired []*model.Signal
	for id, s := range e.active {
		total := s.ExpiresAt.Sub(s.CreatedAt)
		if total <= 0 {
			delete(e.active, id)
			continue
		}
		remaining := s.ExpiresAt.Sub(now)
		if remaining <= 0 {
			s.Status = model.SignalExpired
			s.Decay = 0
			expired = append(expired, s)
			delete(e.active, id)
			continue
		}
		s.Decay = float64(remaining) / float64(total)
	}
	return expired
}

// TrackTrade 记录已成交交易（把地址行为写入画像库，由调用方提供 store）。
func (e *SignalEngine) securityPass(rep *model.SecurityReport) (bool, string) {
	if rep == nil {
		if e.filter.RejectHoneypot {
			return false, "无安全报告（保守拒绝）"
		}
		return true, ""
	}
	if e.filter.RejectHoneypot && rep.IsHoneypot {
		return false, "蜜罐合约"
	}
	if e.filter.RejectMintable && rep.HasMint {
		return false, "存在增发权限"
	}
	if e.filter.RejectWithBlacklist && rep.HasBlacklist {
		return false, "存在黑名单权限"
	}
	if e.filter.RequireOpenSource && !rep.IsOpenSource {
		return false, "合约未开源"
	}
	if e.filter.MaxBuyTax > 0 && rep.BuyTax > e.filter.MaxBuyTax {
		return false, fmt.Sprintf("买入税过高 %.2f", rep.BuyTax)
	}
	if e.filter.MaxSellTax > 0 && rep.SellTax > e.filter.MaxSellTax {
		return false, fmt.Sprintf("卖出税过高 %.2f", rep.SellTax)
	}
	return true, ""
}

func (e *SignalEngine) recordFiltered(token, reason string) {
	if e.reg != nil {
		e.reg.Inc("memebot_signals_filtered_total", 1)
	}
	e.log.Debug("signal filtered", zap.String("token", token), zap.String("reason", reason))
}

func (e *SignalEngine) trackBuyer(token, buyer string, at time.Time) {
	if strings.TrimSpace(buyer) == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	m := e.buyers[token]
	if m == nil {
		m = make(map[string]time.Time)
		e.buyers[token] = m
	}
	m[buyer] = at

	// 控制内存增长：超过 500 个代币时清理最旧的
	if len(e.buyers) > 500 {
		cutoff := e.now().UTC().Add(-2 * time.Hour)
		for k, v := range e.buyers {
			var newest time.Time
			for _, t := range v {
				if t.After(newest) {
					newest = t
				}
			}
			if newest.Before(cutoff) {
				delete(e.buyers, k)
			}
		}
	}
}

func (e *SignalEngine) drop(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.active, id)
}

// volumeMultipleFor 计算成交额突增倍数：优先滚动窗口（相对该代币自身近期均值），
// 样本不足时先记录观测并返回 0（表示"暂不判定"）；未注入窗口时回退为比值近似。
func (e *SignalEngine) volumeMultipleFor(liq *model.LiquidityInfo, chain, token string, at time.Time) float64 {
	if liq == nil || liq.Volume24hUSD <= 0 {
		return 0
	}
	if e.rolling != nil {
		key := chain + "|" + token
		if mult := e.rolling.Multiple(key, liq.Volume24hUSD, 3); mult > 0 {
			return mult
		}
		e.rolling.Observe(key, liq.Volume24hUSD, at)
		return 0
	}
	return e.volumeMultiple(liq)
}

// volumeMultiple 用 24h 成交量与流动性的比值近似“成交活跃度倍数”。
//
// 真实实现应基于滚动均值（需要时序库）；此处给出可用的近似口径并在文档中标注。
func (e *SignalEngine) volumeMultiple(liq *model.LiquidityInfo) float64 {
	if liq == nil || liq.LiquidityUSD <= 0 {
		return 0
	}
	return liq.Volume24hUSD / liq.LiquidityUSD
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
